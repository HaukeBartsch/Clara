package admin

import (
	"context"
	"database/sql"
	"testing"

	"csms/api/internal/db"
)

// mustChoiceField inserts a dropdown carrying the code$label encoding (REQ-DB-014).
func (e *env) mustChoiceField(projectID, instrumentID int64, name, choices string, position int) int64 {
	e.t.Helper()
	id := e.mustField(projectID, instrumentID, name, "dropdown", position)
	if _, err := e.Store.DB.ExecContext(context.Background(),
		`UPDATE fields SET choices = ? WHERE id = ?`, choices, id); err != nil {
		e.t.Fatalf("choices: %v", err)
	}
	return id
}

// These constructors build the storage rows the snapshot mutators take.

func newTextField(name string) db.Field {
	return db.Field{FieldName: name, FieldType: "text"}
}

func newArm(armNum int, name string) db.Arm {
	return db.Arm{ArmNum: armNum, Name: sql.NullString{String: name, Valid: true}}
}

func newEvent(label string, armNum int) db.Event {
	return db.Event{EventName: label, UniqueEventName: uniqueEventName(label, armNum)}
}

func newInstrument(name string) db.Instrument {
	return db.Instrument{Name: name}
}

// classifyCase is one row of the breaking-change table of
// API_Endpoints_Design.md §4.21.
type classifyCase struct {
	name string
	// prepare seeds stored data or extra design rows before the diff is taken.
	prepare func(e *env, projectID int64, f designFixture)
	// mutate applies the staged change to a copy of the active design.
	mutate func(t *testing.T, d *stagedDesign, f designFixture)
	// wantKind/wantBreaking name the entry under test.
	wantKind     string
	wantBreaking bool
	// allowOtherBreaking is set where the change necessarily breaks more than
	// the entry under test (deleting an instrument also deletes its fields).
	allowOtherBreaking bool
}

// TestClassifyDesignChange walks the §4.21 table row by row: the classification
// drives the commit rejection, the staging warning list and the analysis-mode
// acknowledgement, so each row has to be exactly as documented (REQ-API-108).
func TestClassifyDesignChange(t *testing.T) {
	cases := []classifyCase{
		// --- non-breaking rows ---
		{
			name: "add a field",
			mutate: func(t *testing.T, d *stagedDesign, f designFixture) {
				inst, _ := d.instrument(f.instrumentA)
				if _, ok := d.putField(inst.ID, newTextField("note_text")); !ok {
					t.Fatal("putField failed")
				}
			},
			wantKind: "field_added",
		},
		{
			name: "add an arm",
			mutate: func(t *testing.T, d *stagedDesign, f designFixture) {
				d.putArm(newArm(d.nextArmNum(), "Third"))
			},
			wantKind: "arm_added",
		},
		{
			name: "add an event",
			mutate: func(t *testing.T, d *stagedDesign, f designFixture) {
				if _, ok := d.putEvent(f.arm1, newEvent("Screening", 1)); !ok {
					t.Fatal("putEvent failed")
				}
			},
			wantKind: "event_added",
		},
		{
			name: "add an instrument",
			mutate: func(t *testing.T, d *stagedDesign, f designFixture) {
				d.putInstrument(newInstrument("visits"))
			},
			wantKind: "instrument_added",
		},
		{
			name: "map an instrument to an event",
			mutate: func(t *testing.T, d *stagedDesign, f designFixture) {
				d.setMappingForArm(2, map[string][]string{"intake": {"followup_arm_2"}, "labs": {"followup_arm_2"}})
			},
			wantKind: "mapped",
		},
		{
			name: "unmap a pair that holds no values",
			mutate: func(t *testing.T, d *stagedDesign, f designFixture) {
				d.setMappingForArm(2, map[string][]string{"labs": {}})
			},
			wantKind: "unmapped",
		},
		{
			// The master spec's "Unmap is misclassified" row: nothing is deleted,
			// the values stay reachable when the pair is mapped back.
			name: "unmap a pair whose records do hold values",
			prepare: func(e *env, projectID int64, f designFixture) {
				e.mustValue(projectID, "REC001", "followup_arm_2", "", "hb", "9.1")
			},
			mutate: func(t *testing.T, d *stagedDesign, f designFixture) {
				d.setMappingForArm(2, map[string][]string{"labs": {}})
			},
			wantKind: "unmapped",
		},
		{
			name: "change a field label and note",
			mutate: func(t *testing.T, d *stagedDesign, f designFixture) {
				fld, _ := d.fieldByName("age")
				fld.FieldLabel = sql.NullString{String: "Age at entry", Valid: true}
				fld.FieldNote = sql.NullString{String: "Completed by nurse", Valid: true}
			},
			wantKind: "field_updated",
		},
		{
			name: "reorder fields",
			mutate: func(t *testing.T, d *stagedDesign, f designFixture) {
				inst, _ := d.instrument(f.instrumentA)
				ids := make([]int64, 0, len(inst.Fields))
				for i := len(inst.Fields) - 1; i >= 0; i-- {
					ids = append(ids, inst.Fields[i].ID)
				}
				if !d.orderFields(inst.ID, ids) {
					t.Fatal("orderFields rejected the order")
				}
			},
			wantKind: "reordered",
		},
		{
			name: "rename a field (values follow)",
			mutate: func(t *testing.T, d *stagedDesign, f designFixture) {
				fld, _ := d.fieldByName("age")
				fld.FieldName = "age_at_entry"
			},
			wantKind: "field_renamed",
		},
		{
			name: "add options to an existing dropdown",
			prepare: func(e *env, projectID int64, f designFixture) {
				e.mustChoiceField(projectID, f.instrumentA, "colour", "1$Red##2$Green", 4)
			},
			mutate: func(t *testing.T, d *stagedDesign, f designFixture) {
				fld, _ := d.fieldByName("colour")
				fld.Choices = sql.NullString{String: "1$Red##2$Green##3$Blue", Valid: true}
			},
			wantKind: "choices_extended",
		},
		{
			name: "remove an option no value uses",
			prepare: func(e *env, projectID int64, f designFixture) {
				e.mustChoiceField(projectID, f.instrumentA, "colour", "1$Red##2$Green##3$Blue", 4)
				e.mustValue(projectID, "REC001", "baseline_arm_1", "", "colour", "1")
			},
			mutate: func(t *testing.T, d *stagedDesign, f designFixture) {
				fld, _ := d.fieldByName("colour")
				fld.Choices = sql.NullString{String: "1$Red##2$Green", Valid: true}
			},
			wantKind: "choices_extended",
		},
		{
			name: "tighten validation the stored values still satisfy",
			prepare: func(e *env, projectID int64, f designFixture) {
				e.mustValue(projectID, "REC001", "baseline_arm_1", "", "age", "42")
			},
			mutate: func(t *testing.T, d *stagedDesign, f designFixture) {
				fld, _ := d.fieldByName("age")
				fld.ValidationType = sql.NullString{String: "integer", Valid: true}
			},
			wantKind: "field_updated",
		},

		// --- breaking rows ---
		{
			name: "delete a field",
			mutate: func(t *testing.T, d *stagedDesign, f designFixture) {
				fld, _ := d.fieldByName("hb")
				if !d.deleteField(fld.ID) {
					t.Fatal("deleteField failed")
				}
			},
			wantKind:     "field_deleted",
			wantBreaking: true,
		},
		{
			name: "change a field type",
			mutate: func(t *testing.T, d *stagedDesign, f designFixture) {
				fld, _ := d.fieldByName("age")
				fld.FieldType = "dropdown"
				fld.Choices = sql.NullString{String: "1$Young##2$Old", Valid: true}
			},
			wantKind:           "field_type_changed",
			wantBreaking:       true,
			allowOtherBreaking: true,
		},
		{
			name: "tighten validation past the stored values",
			prepare: func(e *env, projectID int64, f designFixture) {
				e.mustValue(projectID, "REC001", "baseline_arm_1", "", "age", "42")
			},
			mutate: func(t *testing.T, d *stagedDesign, f designFixture) {
				fld, _ := d.fieldByName("age")
				fld.ValidationType = sql.NullString{String: "MRN", Valid: true} // ^[0-9]{11}$
			},
			wantKind:     "validation_tightened",
			wantBreaking: true,
		},
		{
			name: "remove a choice option that stored values use",
			prepare: func(e *env, projectID int64, f designFixture) {
				e.mustChoiceField(projectID, f.instrumentA, "colour", "1$Red##2$Green##3$Blue", 4)
				e.mustValue(projectID, "REC001", "baseline_arm_1", "", "colour", "3")
			},
			mutate: func(t *testing.T, d *stagedDesign, f designFixture) {
				fld, _ := d.fieldByName("colour")
				fld.Choices = sql.NullString{String: "1$Red##2$Green", Valid: true}
			},
			wantKind:     "choice_removed",
			wantBreaking: true,
		},
		{
			name: "re-code an existing option",
			prepare: func(e *env, projectID int64, f designFixture) {
				e.mustChoiceField(projectID, f.instrumentA, "colour", "1$Red##2$Green", 4)
				e.mustValue(projectID, "REC001", "baseline_arm_1", "", "colour", "1")
			},
			mutate: func(t *testing.T, d *stagedDesign, f designFixture) {
				fld, _ := d.fieldByName("colour")
				fld.Choices = sql.NullString{String: "1$Crimson##2$Green", Valid: true}
			},
			wantKind:     "choice_recoded",
			wantBreaking: true,
		},
		{
			name: "delete an instrument that holds data",
			prepare: func(e *env, projectID int64, f designFixture) {
				e.mustValue(projectID, "REC001", "baseline_arm_1", "", "hb", "9.1")
			},
			mutate: func(t *testing.T, d *stagedDesign, f designFixture) {
				d.deleteInstrument(f.instrumentB)
			},
			wantKind:           "instrument_deleted",
			wantBreaking:       true,
			allowOtherBreaking: true,
		},
		{
			name: "delete an event that holds data",
			prepare: func(e *env, projectID int64, f designFixture) {
				e.mustValue(projectID, "REC001", "followup_arm_2", "", "hb", "9.1")
			},
			mutate: func(t *testing.T, d *stagedDesign, f designFixture) {
				if !d.deleteEvent(f.event2) {
					t.Fatal("deleteEvent failed")
				}
			},
			wantKind:     "event_deleted",
			wantBreaking: true,
		},
		{
			name: "delete an arm with data",
			prepare: func(e *env, projectID int64, f designFixture) {
				e.mustValue(projectID, "REC001", "followup_arm_2", "", "hb", "9.1")
			},
			mutate: func(t *testing.T, d *stagedDesign, f designFixture) {
				d.deleteArm(f.arm2)
			},
			wantKind:     "arm_deleted",
			wantBreaking: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			ctx := context.Background()
			projectID := e.mustProject("Classify " + tc.name)
			f := e.seedDesign(projectID)
			if tc.prepare != nil {
				tc.prepare(e, projectID, f)
			}

			active, err := e.Handler.stagedDesignOf(ctx, projectID)
			if err != nil {
				t.Fatalf("stagedDesignOf: %v", err)
			}
			staged, err := active.clone()
			if err != nil {
				t.Fatalf("clone: %v", err)
			}
			tc.mutate(t, staged, f)

			changes, err := classifyDesignChange(ctx, active, staged,
				storeValues{store: e.Store, projectID: projectID})
			if err != nil {
				t.Fatalf("classifyDesignChange: %v", err)
			}

			var found *stagedChange
			for i := range changes {
				t.Logf("%s breaking=%v reason=%q", changes[i].Kind, changes[i].Breaking, changes[i].Reason)
				if changes[i].Kind == tc.wantKind && found == nil {
					found = &changes[i]
				}
			}
			if found == nil {
				t.Fatalf("no %s change in %+v", tc.wantKind, changes)
			}
			if found.Breaking != tc.wantBreaking {
				t.Errorf("%s breaking = %v, want %v (reason %q)",
					tc.wantKind, found.Breaking, tc.wantBreaking, found.Reason)
			}
			if !tc.wantBreaking && !tc.allowOtherBreaking {
				for _, c := range changes {
					if c.Breaking {
						t.Errorf("a non-breaking change reported %s (%s)", c.Kind, c.Object)
					}
				}
			}
		})
	}
}

// TestClassifyNoChangeIsSilent: an untouched snapshot produces no entries at
// all — the staging banner and the commit guard both read "nothing staged".
func TestClassifyNoChangeIsSilent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	projectID := e.mustProject("Silent Diff")
	e.seedDesign(projectID)

	d, err := e.Handler.stagedDesignOf(ctx, projectID)
	if err != nil {
		t.Fatalf("stagedDesignOf: %v", err)
	}
	changes, err := classifyDesignChange(ctx, d, d, storeValues{store: e.Store, projectID: projectID})
	if err != nil {
		t.Fatalf("classifyDesignChange: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("changes = %+v, want none", changes)
	}
}

// TestClassifyInstrumentRenameIsNotRemap keeps a rename from reading as
// unmapping and remapping every pair the instrument held.
func TestClassifyInstrumentRenameIsNotRemap(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	projectID := e.mustProject("Rename Not Remap")
	f := e.seedDesign(projectID)

	active, err := e.Handler.stagedDesignOf(ctx, projectID)
	if err != nil {
		t.Fatalf("stagedDesignOf: %v", err)
	}
	staged, err := active.clone()
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	staged.renameInstrument(f.instrumentB, "bloodwork")

	changes, err := classifyDesignChange(ctx, active, staged,
		storeValues{store: e.Store, projectID: projectID})
	if err != nil {
		t.Fatalf("classifyDesignChange: %v", err)
	}
	for _, c := range changes {
		if c.Kind == "mapped" || c.Kind == "unmapped" {
			t.Errorf("rename reported a mapping change (%s %s)", c.Kind, c.Object)
		}
		if c.Breaking {
			t.Errorf("rename reported a breaking change (%s %s)", c.Kind, c.Object)
		}
	}
	var renamed bool
	for _, c := range changes {
		if c.Kind == "instrument_renamed" {
			renamed = true
		}
	}
	if !renamed {
		t.Errorf("no instrument_renamed entry in %+v", changes)
	}
}
