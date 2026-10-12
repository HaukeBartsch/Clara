package validate

import "testing"

func TestNormalizeChoices(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"1$Yes##0$No", "1$Yes##0$No"},
		{"1, Yes | 0, No", "1$Yes##0$No"},
		// Labels keep their commas: only the first comma splits a choice.
		{"0, Remove PR | 1, Never anonymize (dangerous, full responsibility)",
			"0$Remove PR##1$Never anonymize (dangerous, full responsibility)"},
		// Text codes, and a label that differs from its code.
		{"PECTMRI, PECTMRI | QC-NHPhantom, QC - NH Phantom", "PECTMRI$PECTMRI##QC-NHPhantom$QC - NH Phantom"},
		// A label holding $ is still REDCap's form: its code part is not a code.
		{"1, Cost in $ | 2, Free", "1$Cost in $##2$Free"},
		{"1 | 2", "1$1##2$2"},
		{" 1 ,  Spaced  |  ", "1$Spaced"},
		// Neither form: left as written, for the design-time rules to report.
		{"1$yes##x y$no", "1$yes##x y$no"},
		{"a b, c", "a b, c"},
	}
	for _, c := range cases {
		if got := NormalizeChoices(c.in); got != c.want {
			t.Errorf("NormalizeChoices(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
