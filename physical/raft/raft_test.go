package raft

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	fmt "fmt"
	"io"
	"io/ioutil"
	"os"
	"testing"
	"time"

	"github.com/go-test/deep"
	"github.com/golang/protobuf/proto"
	hclog "github.com/hashicorp/go-hclog"
	uuid "github.com/hashicorp/go-uuid"
	"github.com/hashicorp/vault/sdk/physical"
	bolt "go.etcd.io/bbolt"
)

func getRaft(t testing.TB, bootstrap bool, noStoreState bool) (*RaftBackend, string) {
	raftDir, err := ioutil.TempDir("", "vault-raft-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("raft dir: %s", raftDir)

	logger := hclog.New(&hclog.LoggerOptions{
		Name:  "raft",
		Level: hclog.Trace,
	})
	logger.Info("raft dir", "dir", raftDir)

	conf := map[string]string{
		"path": raftDir,
	}

	if noStoreState {
		conf["doNotStoreLatestState"] = ""
	}

	backendRaw, err := NewRaftBackend(conf, logger)
	if err != nil {
		t.Fatal(err)
	}
	backend := backendRaw.(*RaftBackend)

	if bootstrap {
		err = backend.Bootstrap(context.Background(), []Peer{Peer{ID: backend.NodeID(), Address: backend.NodeID()}})
		if err != nil {
			t.Fatal(err)
		}

		err = backend.SetupCluster(context.Background(), nil, &noopClusterHook{
			addr: &idAddr{
				id: backend.NodeID(),
			},
		})
		if err != nil {
			t.Fatal(err)
		}

	}

	return backend, raftDir
}

func compareFSMs(t *testing.T, fsm1, fsm2 *FSM) {
	index1, config1 := fsm1.LatestState()
	index2, config2 := fsm2.LatestState()

	if !proto.Equal(index1, index2) {
		t.Fatalf("indexes did not match: %+v != %+v", index1, index2)
	}
	if !proto.Equal(config1, config2) {
		t.Fatalf("configs did not match: %+v != %+v", config1, config2)
	}

	compareDBs(t, fsm1.db, fsm2.db)
}

func compareDBs(t *testing.T, boltDB1, boltDB2 *bolt.DB) {
	db1 := make(map[string]string)
	db2 := make(map[string]string)

	err := boltDB1.View(func(tx *bolt.Tx) error {

		c := tx.Cursor()
		for bucketName, _ := c.First(); bucketName != nil; bucketName, _ = c.Next() {
			b := tx.Bucket(bucketName)

			cBucket := b.Cursor()

			for k, v := cBucket.First(); k != nil; k, v = cBucket.Next() {
				db1[string(k)] = base64.StdEncoding.EncodeToString(v)
			}
		}

		return nil
	})

	if err != nil {
		t.Fatal(err)
	}

	err = boltDB2.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(dataBucketName)

		c := b.Cursor()

		for k, v := c.First(); k != nil; k, v = c.Next() {
			db2[string(k)] = base64.StdEncoding.EncodeToString(v)
		}

		return nil
	})

	if err != nil {
		t.Fatal(err)
	}

	if diff := deep.Equal(db1, db2); diff != nil {
		t.Fatal(diff)
	}
}

func TestRaft_Backend(t *testing.T) {
	b, dir := getRaft(t, true, true)
	defer os.RemoveAll(dir)

	physical.ExerciseBackend(t, b)
}

func TestRaft_Backend_ListPrefix(t *testing.T) {
	b, dir := getRaft(t, true, true)
	defer os.RemoveAll(dir)

	physical.ExerciseBackend_ListPrefix(t, b)
}

func TestRaft_TransactionalBackend(t *testing.T) {
	b, dir := getRaft(t, true, true)
	defer os.RemoveAll(dir)

	physical.ExerciseTransactionalBackend(t, b)
}

func TestRaft_HABackend(t *testing.T) {
	raft, dir := getRaft(t, true, true)
	defer os.RemoveAll(dir)
	raft2, dir2 := getRaft(t, false, true)
	defer os.RemoveAll(dir2)

	// Add raft2 to the cluster
	addPeer(t, raft, raft2)

	physical.ExerciseHABackend(t, raft, raft2)
}

func TestRaft_Backend_ThreeNode(t *testing.T) {
	raft1, dir := getRaft(t, true, true)
	raft2, dir2 := getRaft(t, false, true)
	raft3, dir3 := getRaft(t, false, true)
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	defer os.RemoveAll(dir3)

	// Add raft2 to the cluster
	addPeer(t, raft1, raft2)

	// Add raft3 to the cluster
	addPeer(t, raft1, raft3)

	physical.ExerciseBackend(t, raft1)

	time.Sleep(10 * time.Second)
	// Make sure all stores are the same
	compareFSMs(t, raft1.fsm, raft2.fsm)
	compareFSMs(t, raft1.fsm, raft3.fsm)
}

func TestRaft_TransactionalBackend_ThreeNode(t *testing.T) {
	raft1, dir := getRaft(t, true, true)
	raft2, dir2 := getRaft(t, false, true)
	raft3, dir3 := getRaft(t, false, true)
	defer os.RemoveAll(dir)
	defer os.RemoveAll(dir2)
	defer os.RemoveAll(dir3)

	// Add raft2 to the cluster
	addPeer(t, raft1, raft2)

	// Add raft3 to the cluster
	addPeer(t, raft1, raft3)

	physical.ExerciseTransactionalBackend(t, raft1)

	time.Sleep(10 * time.Second)
	// Make sure all stores are the same
	compareFSMs(t, raft1.fsm, raft2.fsm)
	compareFSMs(t, raft1.fsm, raft3.fsm)
}

func TestRaft_Backend_MaxSize(t *testing.T) {
	// Set the max size a little lower for the test
	maxCommandSizeBytes = 10 * 1024

	b, dir := getRaft(t, true, true)
	defer os.RemoveAll(dir)

	// Test a value slightly below the max size
	value := make([]byte, maxCommandSizeBytes-100)
	err := b.Put(context.Background(), &physical.Entry{
		Key:   "key",
		Value: value,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Test value at max size, should error
	value = make([]byte, maxCommandSizeBytes)
	err = b.Put(context.Background(), &physical.Entry{
		Key:   "key",
		Value: value,
	})
	if err != ErrCommandTooLarge {
		t.Fatal(err)
	}
}

func BenchmarkDB_Puts(b *testing.B) {
	raft, dir := getRaft(b, true, false)
	defer os.RemoveAll(dir)
	raft2, dir2 := getRaft(b, true, false)
	defer os.RemoveAll(dir2)

	bench := func(b *testing.B, s physical.Backend, dataSize int) {
		data, err := uuid.GenerateRandomBytes(dataSize)
		if err != nil {
			b.Fatal(err)
		}

		ctx := context.Background()
		pe := &physical.Entry{
			Value: data,
		}
		testName := b.Name()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			pe.Key = fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("%s-%d", testName, i))))
			err := s.Put(ctx, pe)
			if err != nil {
				b.Fatal(err)
			}
		}
	}

	b.Run("256b", func(b *testing.B) { bench(b, raft, 256) })
	b.Run("256kb", func(b *testing.B) { bench(b, raft2, 256*1024) })
}

func BenchmarkDB_Snapshot(b *testing.B) {
	raft, dir := getRaft(b, true, false)
	defer os.RemoveAll(dir)

	data, err := uuid.GenerateRandomBytes(256 * 1024)
	if err != nil {
		b.Fatal(err)
	}

	ctx := context.Background()
	pe := &physical.Entry{
		Value: data,
	}
	testName := b.Name()

	for i := 0; i < 100; i++ {
		pe.Key = fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("%s-%d", testName, i))))
		err = raft.Put(ctx, pe)
		if err != nil {
			b.Fatal(err)
		}
	}

	bench := func(b *testing.B, s *FSM) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			pe.Key = fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("%s-%d", testName, i))))
			s.writeTo(ctx, discardCloser{Writer: ioutil.Discard}, discardCloser{Writer: ioutil.Discard})
		}
	}

	b.Run("256kb", func(b *testing.B) { bench(b, raft.fsm) })
}

type discardCloser struct {
	io.Writer
}

func (d discardCloser) Close() error               { return nil }
func (d discardCloser) CloseWithError(error) error { return nil }