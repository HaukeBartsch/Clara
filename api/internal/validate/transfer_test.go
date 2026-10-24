package validate

import (
	"strings"
	"testing"
)

func TestParseTransferConfig(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    []TransferOp
		wantErr string // substring; empty means expect success
	}{
		{
			name: "vision example",
			in: "# create a new record in [project] with record name that gets the value of the current instrument's [field]\n" +
				"create_record [studyb][record_id] [id_field]\n" +
				"# copy the value of the current instruments field to the new records [event][place_for_field1]\n" +
				"copy [age]  [studyb][baseline][place_for_field1]\n" +
				"copy age2 [studyb][baseline][place_for_field2]\n",
			want: []TransferOp{
				{Kind: "create_record", TargetProject: "studyb", RecordIDField: "record_id", SourceField: "id_field"},
				{Kind: "copy", SourceField: "age", TargetProject: "studyb", Event: "baseline", TargetField: "place_for_field1"},
				{Kind: "copy", SourceField: "age2", TargetProject: "studyb", Event: "baseline", TargetField: "place_for_field2"},
			},
		},
		{
			name: "event-less target arity",
			in:   "create_record [p][record_id] [x]\ncopy [x] [p][y]",
			want: []TransferOp{
				{Kind: "create_record", TargetProject: "p", RecordIDField: "record_id", SourceField: "x"},
				{Kind: "copy", SourceField: "x", TargetProject: "p", TargetField: "y"},
			},
		},
		{
			name: "unique event name kept verbatim",
			in:   "create_record [p][record_id] [x]\ncopy [x] [p][visit_2_arm_3][y]",
			want: []TransferOp{
				{Kind: "create_record", TargetProject: "p", RecordIDField: "record_id", SourceField: "x"},
				{Kind: "copy", SourceField: "x", TargetProject: "p", Event: "visit_2_arm_3", TargetField: "y"},
			},
		},
		{name: "empty config", in: "", wantErr: "no operations"},
		{name: "comments only", in: "# nothing\n   # here either\n", wantErr: "no operations"},
		{name: "copy before create", in: "copy [a] [p][e][b]\ncreate_record [p][record_id] [x]", wantErr: "copy before the create_record"},
		{name: "two creates", in: "create_record [p][record_id] [x]\ncopy [a] [p][e][b]\ncreate_record [q][record_id] [y]", wantErr: "only one create"},
		{name: "unknown keyword", in: "send [a] [p][e][b]", wantErr: "expected create_record"},
		{name: "bracketed keyword rejected", in: "[create_record] [p][record_id] [x]", wantErr: "expected create_record"},
		{name: "legacy keyword rejected", in: "create [p][record_id] [x]", wantErr: `write create_record or create_project`},
		{name: "create missing source", in: "create_record [p][record_id]",
			wantErr: "create_record takes 2 arguments, not 1: create_record [project][identifier_field] <source_field>"},
		{name: "copy missing target", in: "create_record [p][record_id] [x]\ncopy [a]",
			wantErr: "copy takes 2 arguments, not 1: copy <source_field> [project][event][target_field]"},
		{name: "bare target project rejected", in: "create_record [p][record_id] [x]\ncopy [a] p[e][b]", wantErr: "must be written in [brackets]"},
		{name: "unexpected character", in: "create_record [p][record_id] x!", wantErr: "unexpected character"},
		{name: "copy target one group", in: "create_record [p][record_id] [x]\ncopy [a] [b]", wantErr: "copy target must be"},
		{name: "copy target four groups", in: "create_record [p][record_id] [x]\ncopy [a] [p][e][f][g]", wantErr: "copy target must be"},
		{name: "unterminated bracket", in: "create_record [p][record_id [x]", wantErr: "unterminated ["},
		{name: "empty group", in: "create_record [p][] [x]", wantErr: "empty bracket group"},
		{name: "mixed bare and bracketed in target", in: "create_record p[record_id] [x]", wantErr: "must be written in [brackets]"},
		{name: "trailing comment on op line", in: "create_record [p][record_id] [x] # name it\n", want: []TransferOp{
			{Kind: "create_record", TargetProject: "p", RecordIDField: "record_id", SourceField: "x"},
		}},
		{name: "create project with outputs", in: "create_project [project_name] [Template Project] [project_token] [project_id]",
			want: []TransferOp{{Kind: "create_project", SourceField: "project_name", Template: "Template Project",
				TokenField: "project_token", IDField: "project_id"}}},
		{name: "create project with token only", in: "create_project project_name template token_field",
			want: []TransferOp{{Kind: "create_project", SourceField: "project_name", Template: "template",
				TokenField: "token_field"}}},
		{name: "create project cannot copy", in: "create_project project_name template\ncopy age [Target][event][age]",
			wantErr: "cannot be combined"},
		{name: "create project wrong arity", in: "create_project project_name template token id extra",
			wantErr: "create_project takes 1 to 4 arguments, not 5"},
		// The name of the new project is a field reference, and one argument is a
		// whole instruction: no template means an empty project (REQ-VAL-047).
		{name: "create project alone is an empty project", in: "create_project [record]",
			want: []TransferOp{{Kind: "create_project", SourceField: "record"}}},
		{name: "create project name is a reference bare or bracketed", in: "create_project record template",
			want: []TransferOp{{Kind: "create_project", SourceField: "record", Template: "template"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseTransferConfig(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d ops, want %d (%+v)", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("op %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestParseTransferConfigLineNumbers(t *testing.T) {
	_, err := ParseTransferConfig("create_record [p][record_id] [x]\n\ncopy [a] [p][e][b]\nsend bad\n")
	if err == nil || !strings.HasPrefix(err.Error(), "line 4:") {
		t.Fatalf("err = %v, want line 4 prefix", err)
	}
}

func TestParseTransferConfigCaps(t *testing.T) {
	long := "create_record [p][record_id] [x]\n" + strings.Repeat("copy [a] [p][e][b]\n", 300)
	if _, err := ParseTransferConfig(long); err == nil || !strings.Contains(err.Error(), "operations") {
		t.Fatalf("expected op-count error, got %v", err)
	}
	huge := "create_record [p][record_id] [" + strings.Repeat("x", maxTransferConfigLen) + "]"
	if _, err := ParseTransferConfig(huge); err == nil || !strings.Contains(err.Error(), "longer than") {
		t.Fatalf("expected length error, got %v", err)
	}
}
