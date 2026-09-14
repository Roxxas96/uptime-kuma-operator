package annotations

import "testing"

func TestShouldSync(t *testing.T) {
	cases := []struct {
		name     string
		watchAll bool
		ann      map[string]string
		want     bool
	}{
		{"default mode, no annotation", false, nil, false},
		{"default mode, enabled=true", false, map[string]string{Enabled: "true"}, true},
		{"default mode, enabled=false", false, map[string]string{Enabled: "false"}, false},
		{"watch-all, no annotation", true, nil, true},
		{"watch-all, enabled=false opts out", true, map[string]string{Enabled: "false"}, false},
		{"watch-all, enabled=true", true, map[string]string{Enabled: "true"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ShouldSync(c.watchAll, c.ann); got != c.want {
				t.Errorf("ShouldSync(%v, %v) = %v, want %v", c.watchAll, c.ann, got, c.want)
			}
		})
	}
}

func TestParseOverrides(t *testing.T) {
	ov, err := ParseOverrides(map[string]string{
		Name:                "friendly-name",
		Scheme:              "http",
		Interval:            "30",
		RetryInterval:       "10",
		MaxRetries:          "5",
		AcceptedStatusCodes: "200-299, 301",
	})
	if err != nil {
		t.Fatalf("ParseOverrides: %v", err)
	}
	want := Overrides{
		Name: "friendly-name", Scheme: "http",
		Interval: 30, RetryInterval: 10, MaxRetries: 5,
		AcceptedStatusCodes: []string{"200-299", "301"},
	}
	if ov.Name != want.Name || ov.Scheme != want.Scheme || ov.Interval != want.Interval ||
		ov.RetryInterval != want.RetryInterval || ov.MaxRetries != want.MaxRetries ||
		len(ov.AcceptedStatusCodes) != 2 || ov.AcceptedStatusCodes[0] != "200-299" || ov.AcceptedStatusCodes[1] != "301" {
		t.Errorf("ParseOverrides = %+v, want %+v", ov, want)
	}
}

func TestParseOverrides_InvalidScheme(t *testing.T) {
	if _, err := ParseOverrides(map[string]string{Scheme: "ftp"}); err == nil {
		t.Fatal("expected error for invalid scheme, got nil")
	}
}

func TestParseOverrides_InvalidInteger(t *testing.T) {
	if _, err := ParseOverrides(map[string]string{Interval: "not-a-number"}); err == nil {
		t.Fatal("expected error for non-integer interval, got nil")
	}
}

func TestMonitorIDsRoundTrip(t *testing.T) {
	ann := map[string]string{}
	ann = SetMonitorIDs(ann, map[string]string{"a.example.com": "1", "b.example.com": "2"})

	got, err := ParseMonitorIDs(ann)
	if err != nil {
		t.Fatalf("ParseMonitorIDs: %v", err)
	}
	if got["a.example.com"] != "1" || got["b.example.com"] != "2" {
		t.Errorf("ParseMonitorIDs = %v, want {a.example.com:1 b.example.com:2}", got)
	}
}

func TestSetMonitorIDs_EmptyRemovesAnnotation(t *testing.T) {
	ann := map[string]string{MonitorIDs: `{"a":"1"}`}
	ann = SetMonitorIDs(ann, map[string]string{})
	if _, ok := ann[MonitorIDs]; ok {
		t.Error("MonitorIDs annotation still present after clearing to empty map")
	}
}

func TestParseMonitorIDs_MissingAnnotationIsEmptyMap(t *testing.T) {
	got, err := ParseMonitorIDs(nil)
	if err != nil {
		t.Fatalf("ParseMonitorIDs: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ParseMonitorIDs(nil) = %v, want empty map", got)
	}
}
