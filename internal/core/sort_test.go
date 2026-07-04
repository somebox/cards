package core

import "testing"

func TestParseSort(t *testing.T) {
	cases := []struct {
		in       string
		wantErr  bool
		field    string
		desc     bool
		wantZero bool
	}{
		{in: "", wantZero: true},
		{in: "title", field: "title"},
		{in: "-title", field: "title", desc: true},
		{in: "created_at", field: "created_at"},
		{in: "-created_at", field: "created_at", desc: true},
		{in: "updated_at", field: "updated_at"},
		{in: "-updated_at", field: "updated_at", desc: true},
		{in: "fields.priority", field: "fields.priority"},
		{in: "-fields.priority", field: "fields.priority", desc: true},
		{in: "fields.story_points", field: "fields.story_points"},
		// Rejected: unknown key, injection attempt, bad field id.
		{in: "owner", wantErr: true},
		{in: "id", wantErr: true},
		{in: "title; DROP TABLE cards", wantErr: true},
		{in: "fields.a-b", wantErr: true},
		{in: "fields.", wantErr: true},
		{in: "fields.a.b", wantErr: true},
	}
	for _, c := range cases {
		got, err := ParseSort(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseSort(%q): expected error, got %+v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseSort(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got.IsZero() != c.wantZero {
			t.Errorf("ParseSort(%q).IsZero() = %v, want %v", c.in, got.IsZero(), c.wantZero)
		}
		if got.Field != c.field || got.Desc != c.desc {
			t.Errorf("ParseSort(%q) = {Field:%q Desc:%v}, want {Field:%q Desc:%v}", c.in, got.Field, got.Desc, c.field, c.desc)
		}
	}
}
