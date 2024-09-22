package gotenberg

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dlclark/regexp2"
)

// ErrFiltered happens if a value is filtered by the [FilterDeadline] function.
var ErrFiltered = errors.New("value filtered")

var (
	adBlockOnce    sync.Once
	adBlockRegExps []*regexp2.Regexp
)

// FilterDeadline checks if given value is allowed and not denied according to
// regex patterns. It returns a [context.DeadlineExceeded] if it takes too long
// to process.
func FilterDeadline(allowed, denied *regexp2.Regexp, adBlocked []string, s string, deadline time.Time) error {
	// FIXME: not ideal to compile everytime, but is there another way to create a clone?
	if allowed.String() != "" {
		allow := regexp2.MustCompile(allowed.String(), 0)
		allow.MatchTimeout = time.Until(deadline)

		ok, err := allow.MatchString(s)
		if err != nil {
			if time.Now().After(deadline) {
				return context.DeadlineExceeded
			}
			return fmt.Errorf("'%s' cannot handle '%s': %w", allow.String(), s, err)
		}
		if !ok {
			return fmt.Errorf("'%s' does not match the expression from the allowed list: %w", s, ErrFiltered)
		}
	}

	if denied.String() != "" {
		deny := regexp2.MustCompile(denied.String(), 0)
		deny.MatchTimeout = time.Until(deadline)

		ok, err := deny.MatchString(s)
		if err != nil {
			if time.Now().After(deadline) {
				return context.DeadlineExceeded
			}
			return fmt.Errorf("'%s' cannot handle '%s': %w", deny.String(), s, err)
		}
		if ok {
			return fmt.Errorf("'%s' matches the expression from the denied list: %w", s, ErrFiltered)
		}
	}

	return nil
}

func FilterAdBlockDeadline(blocked []string, s string, deadline time.Time) error {
	adBlockOnce.Do(func() {
		for _, b := range blocked {
			adBlockRegExps = append(adBlockRegExps, regexp2.MustCompile(fmt.Sprintf(`.*%s.*`, b), 0))
		}

		for _, b := range adBlockRegExps {
			fmt.Println(b.String())
		}
	})

	for _, r := range adBlockRegExps {
		ok, err := r.MatchString(s)
		if err != nil {
			return fmt.Errorf("'%s' cannot handle '%s': %w", r.String(), s, err)
		}
		if ok {
			return fmt.Errorf("'%s' matches expression from adblock list '%s': %w", s, r.String(), ErrFiltered)
		}
	}

	return nil
}
