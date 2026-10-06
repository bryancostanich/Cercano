package policy

import "testing"

func TestReconciliationRejectsNativeComparisonOverflowAndNoncanonicalVersion(t *testing.T) {
	for _, v := range []string{"18446744073709551615.0.0", "9223372036854775808.0.0", "01.2.3"} {
		if got := ReconcilePackageVersion(v, "1.2.3"); got.Status != ReconcileInvalid {
			t.Errorf("%q accepted: %+v", v, got)
		}
	}
}
