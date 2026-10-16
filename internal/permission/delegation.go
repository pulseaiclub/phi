package permission

import "context"

// Intersect requires both gates to permit an operation. Nil imposes no extra
// constraint. Gates are evaluated as supplied, including custom gates and live
// BypassGate toggles; bypassing one gate never bypasses the other.
func Intersect(parent, child Gate) Gate {
	if parent == nil {
		return child
	}
	if child == nil {
		return parent
	}
	return intersectGate{parent: parent, child: child}
}

type intersectGate struct {
	parent Gate
	child  Gate
}

func (g intersectGate) Check(ctx context.Context, req Request) (Decision, string) {
	parent, parentReason := g.parent.Check(ctx, req)
	if parent != Allow && parent != Ask {
		return Deny, parentReason
	}
	child, childReason := g.child.Check(ctx, req)
	if child != Allow && child != Ask {
		return Deny, childReason
	}
	if parent == Ask {
		return Ask, parentReason
	}
	return child, childReason
}
