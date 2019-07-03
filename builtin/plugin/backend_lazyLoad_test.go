package plugin

import (
	"context"
	"testing"

	"github.com/hashicorp/vault/sdk/logical"
)

func TestBackend_startBackend(t *testing.T) {

	var err error

	ctx := context.Background()
	//logger := logging.NewVaultLogger(log.Trace)
	storage := &logical.InmemStorage{}

	config := &logical.BackendConfig{
		Config: map[string]string{
			"plugin_name": "test-plugin",
			"plugin_type": "secret",
		},
	}

	//meta, err := plugin.NewBackend(
	//	ctx, "test-plugin", consts.PluginTypeSecrets, config.System, config, true)

	b := &PluginBackend{
		//Backend: meta,
		config: config,
	}

	err = b.foo(ctx, storage)
	if err != nil {
		t.Fatal(err)
	}

}
