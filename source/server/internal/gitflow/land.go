package gitflow

import "context"

// Divergence summarizes how a feature branch relates to trunk.
type Divergence struct {
	TrunkAhead      int  // commits on trunk not on feature
	FeatureAhead    int  // commits on feature not on trunk
	FastForwardable bool // trunk is already an ancestor of feature
}

// Divergence reports the commit gap between feature and trunk.
func (r *Repo) Divergence(ctx context.Context, feature, trunk string) (Divergence, error) {
	var d Divergence
	base, err := r.MergeBase(ctx, feature, trunk)
	if err != nil {
		return d, err
	}
	if d.TrunkAhead, err = r.CommitsBetween(ctx, base, trunk); err != nil {
		return d, err
	}
	if d.FeatureAhead, err = r.CommitsBetween(ctx, base, feature); err != nil {
		return d, err
	}
	if d.FastForwardable, err = r.IsAncestor(ctx, trunk, feature); err != nil {
		return d, err
	}
	return d, nil
}
