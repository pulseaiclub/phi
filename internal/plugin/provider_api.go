package plugin

import (
	"fmt"

	"github.com/pulseaiclub/lug"
	"github.com/pulseaiclub/lug/runtime"

	"github.com/pulseaiclub/phi/internal/plugin/lib"
)

func installProviderAPI(env *lug.Env, register *RegisterProvider) {
	provider := lug.NewTable()
	provider.Set(lug.String("register"), runtime.WrapFunc(func(v lug.Value) error {
		p, err := lib.ParseProvider(v)
		if err == nil {
			err = register.Register(p)
		}
		if err != nil {
			return fmt.Errorf("phi.provider.register: %w", err)
		}
		return nil
	}))
	oauth2 := lug.NewTable()
	oauth2.Set(lug.String("pkce"), runtime.WrapFunc(func(v lug.Value) (lug.Value, error) {
		p, err := lib.ParsePKCE(v)
		if err != nil {
			return lug.Nil, fmt.Errorf("phi.oauth2.pkce: %w", err)
		}
		return lug.ValueOf(&p), nil
	}))
	phi := lug.NewTable()
	phi.Set(lug.String("provider"), lug.TableVal(provider))
	phi.Set(lug.String("oauth2"), lug.TableVal(oauth2))
	env.Set("phi", lug.TableVal(phi))
}
