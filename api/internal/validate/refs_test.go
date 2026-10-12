package validate

import "testing"

func TestRenameRefs(t *testing.T) {
	boxes := map[string]bool{"feat___1": true, "feat___2": true}
	isBox := func(name string) bool { return boxes[name] }
	renamed := map[string]string{"record_id": "record_id_2", "feat": "feat_2", "age": "age_2"}
	cases := []struct{ in, want string }{
		{`[record_id] = '1'`, `[record_id_2] = '1'`},
		{`[baseline_arm_1][age] >= 18 and [other] = 2`, `[baseline_arm_1][age_2] >= 18 and [other] = 2`},
		{`[feat(1)] = '1' or [feat___2] = 1`, `[feat_2(1)] = '1' or [feat_2___2] = 1`},
		// Brackets inside a string literal are text, in either quote.
		{`[age] = "[age]" || [age] = '[age]'`, `[age_2] = "[age]" || [age_2] = '[age]'`},
		{`text_contains([age], "it's")`, `text_contains([age_2], "it's")`},
		// A field whose name merely ends in ___x is not a box unless the dictionary says so.
		{`[age___x] = 1`, `[age___x] = 1`},
		{`[unrelated] = 1`, `[unrelated] = 1`},
	}
	for _, c := range cases {
		if got := RenameRefs(c.in, renamed, isBox); got != c.want {
			t.Errorf("RenameRefs(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestQualifyRefs(t *testing.T) {
	cases := []struct{ in, want string }{
		{`[a] + [b] * 2`, `[baseline_arm_1][a] + [baseline_arm_1][b] * 2`},
		{`[visit_2_arm_1][a] / [b]`, `[visit_2_arm_1][a] / [baseline_arm_1][b]`},
		{`[feat(1)] + [feat(2)]`, `[baseline_arm_1][feat___1] + [baseline_arm_1][feat___2]`},
	}
	for _, c := range cases {
		got := QualifyRefs(c.in, "baseline_arm_1")
		if got != c.want {
			t.Errorf("QualifyRefs(%q) = %q, want %q", c.in, got, c.want)
		}
		if _, err := ParseCalcExpression(got); err != nil {
			t.Errorf("QualifyRefs(%q) = %q does not parse as a calculation: %v", c.in, got, err)
		}
	}
}

// The REDCap spellings of REQ-VAL-044 parse in branching logic and stay
// errors in the calculation grammar, which keeps its own shape (§6.1).
func TestREDCapSpellingsOnlyInBranching(t *testing.T) {
	ok := func(Ref) bool { return true }
	for _, expr := range []string{
		`[project_annotation_type] = '2' or [project_annotation_type] = '4'`,
		`[project_use_autoid] = '1'`,
		`[a] <> '' AND [b] = 'x'`,
		`[ev][feat(1)] = '1' Or [feat(2)] = "0"`,
	} {
		if err := ValidateBranching(expr, ok); err != nil {
			t.Errorf("ValidateBranching(%q): %v", expr, err)
		}
	}
	for _, expr := range []string{`[e][a] + 'x'`, `[e][a] and [e][b]`, `[e][feat(1)] + 1`} {
		if refs, err := ParseCalcExpression(expr); err == nil {
			// [e][feat(1)] tokenizes as the field "feat(1)" in the calc grammar — no
			// such field can exist (REQ-VAL-011), so design-time resolution refuses it.
			if len(refs) == 1 && refs[0].Field == "feat(1)" {
				continue
			}
			t.Errorf("ParseCalcExpression(%q) accepted a branching-only spelling", expr)
		}
	}
	refs, err := ParseBranchingRefs(`[feat(1)] = '1' and [ev][feat(2)] <> '0'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs[0] != (Ref{Field: "feat___1"}) || refs[1] != (Ref{Event: "ev", Field: "feat___2"}) {
		t.Errorf("box references = %v, want feat___1 and ev/feat___2", refs)
	}
}
