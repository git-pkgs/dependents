package dependents

import (
	"context"
	"errors"

	gitclone "github.com/git-pkgs/clone"
)

// Checkout prepares repository at destination and returns its HEAD commit.
type Checkout interface {
	Prepare(context.Context, string, string) (string, error)
}

// CheckoutFunc adapts a function to Checkout.
type CheckoutFunc func(context.Context, string, string) (string, error)

func (f CheckoutFunc) Prepare(ctx context.Context, repository, destination string) (string, error) {
	if f == nil {
		return "", errors.New("checkout function is required")
	}
	return f(ctx, repository, destination)
}

// CloneCheckout prepares shallow checkouts with git-pkgs/clone. Set Full for
// full history or Ref to select a branch, tag, or commit.
type CloneCheckout struct {
	Retry gitclone.Retry
	Ref   string
	Full  bool
}

func (c CloneCheckout) Prepare(ctx context.Context, repository, destination string) (string, error) {
	if err := gitclone.Ensure(ctx, c.Retry, repository, destination, c.Ref, c.Full); err != nil {
		return "", err
	}
	return gitclone.Head(ctx, destination), nil
}

// CacheCheckout prepares job-local copies through a git-pkgs/clone cache.
type CacheCheckout struct {
	Cache *gitclone.Cache
	Ref   string
}

func (c CacheCheckout) Prepare(ctx context.Context, repository, destination string) (string, error) {
	if c.Cache == nil {
		return "", errors.New("clone cache is required")
	}
	return c.Cache.Prepare(ctx, repository, c.Ref, destination)
}
