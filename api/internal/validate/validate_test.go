package validate

import "testing"

func TestCSVCell(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		// pass-through
		{"empty", "", ""},
		{"plain text", "hello", "hello"},
		{"negative integer", "-5", "-5"},
		{"signed float", "+3.14", "+3.14"},
		{"leading zero minus", "-0", "-0"},

		// always prefixed
		{"equals", "=CMD()", "'=CMD()"},
		{"at", "@SUM(A1)", "'@SUM(A1)"},

		// +/- only when not a §4 number
		{"plus formula", "+cmd|' /C calc'!A0", "'+cmd|' /C calc'!A0"},
		{"minus text", "-abc", "'-abc"},
		{"minus exponent (outside §4)", "-1e10", "'-1e10"},
		{"minus Inf (outside §4)", "-Inf", "'-Inf"},
		{"minus leading dot (outside §4)", "-.5", "'-.5"},
		{"minus trailing dot (outside §4)", "-5.", "'-5."},

		// whitespace decides, content is preserved
		{"whitespace before equals", "  =1+1", "'  =1+1"},
		{"tab before at", "\t@SUM(A1)", "'\t@SUM(A1)"},
		{"legitimate leading space kept", " hello", " hello"},
		{"whitespace only kept", "   ", "   "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CSVCell(tt.in); got != tt.want {
				t.Errorf("CSVCell(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestValidateValueEmpty(t *testing.T) {
	f := Field{Name: "age", Type: "text", ValidationType: "integer", ValidationMin: "1"}
	if v := ValidateValue(f, ""); v != nil {
		t.Errorf("empty value must be a no-op (REQ-VAL-024), got %v", v)
	}
}

func TestValidateValueNonValueFields(t *testing.T) {
	for _, typ := range []string{"description", "header"} {
		f := Field{Name: "info", Type: typ}
		v := ValidateValue(f, "x")
		if v == nil || v.Code != CodeNonValueField {
			t.Errorf("type %q: got %v, want %s", typ, v, CodeNonValueField)
		}
	}
	f := Field{Name: "bmi", Type: "calculated"}
	v := ValidateValue(f, "23.5")
	if v == nil || v.Code != CodeCalculatedRO {
		t.Errorf("calculated: got %v, want %s", v, CodeCalculatedRO)
	}
}

func TestValidateValueTypes(t *testing.T) {
	tests := []struct {
		name string
		typ  string
		in   string
		ok   bool
	}{
		// integer (REQ-VAL-015)
		{"int ok", "integer", "42", true},
		{"int negative", "integer", "-7", true},
		{"int zero", "integer", "0", true},
		{"int plus sign rejected", "integer", "+7", false},
		{"int decimal rejected", "integer", "1.5", false},
		{"int text rejected", "integer", "abc", false},
		{"int empty digits rejected", "integer", "-", false},

		// floating point (REQ-VAL-016)
		{"float ok", "floating point", "3.14", true},
		{"float signed", "floating point", "-0.5", true},
		{"float plus sign", "floating point", "+2.0", true},
		{"float bare integer", "floating point", "5", true},
		{"float exponent rejected", "floating point", "1e5", false},
		{"float leading dot rejected", "floating point", ".5", false},
		{"float trailing dot rejected", "floating point", "5.", false},
		{"float two dots rejected", "floating point", "1.2.3", false},

		// email (REQ-VAL-017)
		{"email ok", "email", "user.name+tag@example.co", true},
		{"email no domain label rejected", "email", "user@localhost", false},
		{"email no at rejected", "email", "user.example.com", false},
		{"email short tld rejected", "email", "a@b.x", false},

		// MRN (REQ-VAL-018)
		{"mrn 11 digits", "MRN", "12345678901", true},
		{"mrn 10 rejected", "MRN", "1234567890", false},
		{"mrn 12 rejected", "MRN", "123456789012", false},

		// free text and deferred date handling (REQ-VAL-021/019/039)
		{"free text", "", "anything goes", true},
		{"date deferred", "date", "not-a-date", true},
		{"datetime deferred", "datetime", "2026-13-99 99:99", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := Field{Name: "f", Type: "text", ValidationType: tt.typ}
			v := ValidateValue(f, tt.in)
			if tt.ok && v != nil {
				t.Errorf("ValidateValue(%q) = %v, want valid", tt.in, v)
			}
			if !tt.ok && (v == nil || v.Code != CodeTypeInvalid) {
				t.Errorf("ValidateValue(%q) = %v, want %s", tt.in, v, CodeTypeInvalid)
			}
		})
	}
}

func TestValidateValueChoices(t *testing.T) {
	f := Field{
		Name:    "status",
		Type:    "dropdown",
		Choices: "1$Active##2$Inactive##3$Deceased",
	}
	if v := ValidateValue(f, "2"); v != nil {
		t.Errorf("valid code rejected: %v", v)
	}
	v := ValidateValue(f, "9")
	if v == nil || v.Code != CodeChoiceInvalid {
		t.Errorf("invalid code: got %v, want %s", v, CodeChoiceInvalid)
	}
	// labels are not codes (REQ-VAL-022/023)
	if v := ValidateValue(f, "Active"); v == nil || v.Code != CodeChoiceInvalid {
		t.Errorf("label as value: got %v, want %s", v, CodeChoiceInvalid)
	}
	// choices are enforced regardless of validation type
	g := f
	g.ValidationType = "integer"
	if v := ValidateValue(g, "9"); v == nil || v.Code != CodeChoiceInvalid {
		t.Errorf("choice check must precede type: got %v, want %s", v, CodeChoiceInvalid)
	}
}

func TestRangeCheck(t *testing.T) {
	tests := []struct {
		name string
		f    Field
		in   string
		want RuleCode // "" = valid
	}{
		{
			name: "int inside",
			f:    Field{Name: "age", Type: "text", ValidationType: "integer", ValidationMin: "0", ValidationMax: "120"},
			in:   "42",
		},
		{
			name: "int below min",
			f:    Field{Name: "age", Type: "text", ValidationType: "integer", ValidationMin: "0", ValidationMax: "120"},
			in:   "-1", want: CodeRange,
		},
		{
			name: "int above max",
			f:    Field{Name: "age", Type: "text", ValidationType: "integer", ValidationMin: "0", ValidationMax: "120"},
			in:   "121", want: CodeRange,
		},
		{
			name: "int boundary inclusive min",
			f:    Field{Name: "age", Type: "text", ValidationType: "integer", ValidationMin: "0", ValidationMax: "120"},
			in:   "0",
		},
		{
			name: "float above max",
			f:    Field{Name: "score", Type: "text", ValidationType: "floating point", ValidationMin: "0", ValidationMax: "10"},
			in:   "10.5", want: CodeRange,
		},
		{
			name: "float inside",
			f:    Field{Name: "score", Type: "text", ValidationType: "floating point", ValidationMin: "0", ValidationMax: "10"},
			in:   "9.99",
		},
		{
			name: "min only",
			f:    Field{Name: "n", Type: "text", ValidationType: "integer", ValidationMin: "5"},
			in:   "4", want: CodeRange,
		},
		{
			name: "max only",
			f:    Field{Name: "n", Type: "text", ValidationType: "integer", ValidationMax: "5"},
			in:   "6", want: CodeRange,
		},
		{
			// beyond float64's exact-integer range: 9007199254740993 = 2^53+1
			name: "int beyond float precision compared exactly",
			f:    Field{Name: "big", Type: "text", ValidationType: "integer", ValidationMin: "9007199254740993"},
			in:   "9007199254740992", want: CodeRange,
		},
		{
			name: "bounds ignored for other types (REQ-VAL-020)",
			f:    Field{Name: "mrn", Type: "text", ValidationType: "MRN", ValidationMin: "99999999999"},
			in:   "12345678901",
		},
		{
			name: "unparseable bound ignored",
			f:    Field{Name: "n", Type: "text", ValidationType: "integer", ValidationMin: "abc"},
			in:   "-999999999",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := ValidateValue(tt.f, tt.in)
			if tt.want == "" && v != nil {
				t.Errorf("ValidateValue(%q) = %v, want valid", tt.in, v)
			}
			if tt.want != "" && (v == nil || v.Code != tt.want) {
				t.Errorf("ValidateValue(%q) = %v, want %s", tt.in, v, tt.want)
			}
		})
	}
}

func TestViolationError(t *testing.T) {
	v := Violation{Code: CodeTypeInvalid, Message: "value \"abc\" is not a valid integer"}
	if got, want := v.Error(), `TYPE_INVALID: value "abc" is not a valid integer`; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestLabelForCode(t *testing.T) {
	choices := "1$Active##2$Inactive##3$Deceased"
	tests := []struct{ code, want string }{
		{"1", "Active"},
		{"3", "Deceased"},
		{"9", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := LabelForCode(choices, tt.code); got != tt.want {
			t.Errorf("LabelForCode(%q) = %q, want %q", tt.code, got, tt.want)
		}
	}
	// a code stored without a $label falls back to the code itself
	if got := LabelForCode("1$A##2", "2"); got != "2" {
		t.Errorf("LabelForCode bare code = %q, want %q", got, "2")
	}
}

func TestCodeForLabel(t *testing.T) {
	choices := "1$Active##2$Inactive##3$Deceased"
	tests := []struct{ label, want string }{
		{"Active", "1"},
		{"Deceased", "3"},
		{"Missing", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := CodeForLabel(choices, tt.label); got != tt.want {
			t.Errorf("CodeForLabel(%q) = %q, want %q", tt.label, got, tt.want)
		}
	}
}

func TestChoiceCodes(t *testing.T) {
	got := choiceCodes("1$A##2$B##3$C")
	if len(got) != 3 || got[0] != "1" || got[2] != "3" {
		t.Errorf("choiceCodes = %v, want [1 2 3]", got)
	}
	// labels containing $ keep everything after the first separator
	got = choiceCodes("1$A$B##2$C")
	if len(got) != 2 || got[0] != "1" {
		t.Errorf("choiceCodes with $ in label = %v, want [1 2]", got)
	}
	if got := choiceCodes(""); got != nil {
		t.Errorf("choiceCodes(\"\") = %v, want nil", got)
	}
}
