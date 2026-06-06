package store

import "encoding/json"

// Helper pair for the JSON-as-string columns we use for list fields in
// the Settings table. SQLite doesn't have a native array type, and
// dragging in a separate table for two or three SSIDs is overkill — so
// we serialize as JSON and column-validate via the getter.

func jsonMarshalStrings(v []string) (string, error) {
	if v == nil {
		v = []string{}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "[]", err
	}
	return string(b), nil
}

func jsonUnmarshalStrings(s string, out *[]string) error {
	if s == "" {
		*out = []string{}
		return nil
	}
	return json.Unmarshal([]byte(s), out)
}
