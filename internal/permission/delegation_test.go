package permission_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pulseaiclub/phi/internal/permission"
)

type delegationGate permission.Decision

func (g delegationGate) Check(context.Context, permission.Request) (permission.Decision, string) {
	return permission.Decision(g), permission.Decision(g).String()
}

func TestIntersectDecisions(t *testing.T) {
	for _, parent := range []permission.Decision{permission.Allow, permission.Ask, permission.Deny} {
		for _, child := range []permission.Decision{permission.Allow, permission.Ask, permission.Deny} {
			t.Run(parent.String()+"/"+child.String(), func(t *testing.T) {
				want := permission.Allow
				if parent == permission.Ask || child == permission.Ask {
					want = permission.Ask
				}
				if parent == permission.Deny || child == permission.Deny {
					want = permission.Deny
				}
				gate := permission.Intersect(delegationGate(parent), delegationGate(child))
				got, reason := gate.Check(t.Context(), permission.Request{})
				assert.Equal(t, want, got)
				assert.Equal(t, want.String(), reason)
			})
		}
	}
}

func TestIntersectBypassOnlyAffectsItsOwnGate(t *testing.T) {
	var enabled atomic.Bool
	parent := &permission.BypassGate{Inner: delegationGate(permission.Deny), Enabled: &enabled}
	gate := permission.Intersect(parent, permission.AllowAll{})
	dec, _ := gate.Check(t.Context(), permission.Request{})
	assert.Equal(t, permission.Deny, dec)
	enabled.Store(true)
	dec, _ = gate.Check(t.Context(), permission.Request{})
	assert.Equal(t, permission.Allow, dec)
	gate = permission.Intersect(parent, delegationGate(permission.Deny))
	dec, _ = gate.Check(t.Context(), permission.Request{})
	assert.Equal(t, permission.Deny, dec)
	gate = permission.Intersect(delegationGate(permission.Deny), parent)
	dec, _ = gate.Check(t.Context(), permission.Request{})
	assert.Equal(t, permission.Deny, dec)
}
