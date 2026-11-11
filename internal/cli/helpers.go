package cli

import (
	"context"

	"github.com/incurl/pocketghost/internal/instance"
)

// withApp bootstraps the app, runs fn against the instance service, and always
// tears the app down.
func withApp(_ context.Context, fn func(ctx context.Context, svc *instance.Service) error) error {
	ctx, cancel := withContext()
	defer cancel()

	a, err := bootstrap(ctx)
	if err != nil {
		return err
	}
	defer a.Close()

	return fn(ctx, a.Instances)
}
