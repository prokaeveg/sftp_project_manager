package config

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestProjectGlowSizeJSON(t *testing.T) {
	for _, tc := range []struct{ input, want int }{{0, 100}, {25, 25}, {50, 50}, {100, 100}, {150, 150}, {-1, 100}, {151, 100}} {
		t.Run(fmt.Sprint(tc.input), func(t *testing.T) {
			var project Project
			if err := json.Unmarshal([]byte(fmt.Sprintf(`{"glowSize":%d,"connections":[]}`, tc.input)), &project); err != nil {
				t.Fatal(err)
			}
			normalized, err := NormalizeProject(project)
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(normalized)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if fields["glowSize"] != float64(tc.want) {
				t.Fatalf("glowSize = %v, want %d", fields["glowSize"], tc.want)
			}
		})
	}
}

func TestProjectColorJSON(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{"#c06bdf", "#c06bdf"}, {"", "#517bbd"}, {"red;bad", "#517bbd"}} {
		var project Project
		if err := json.Unmarshal([]byte(`{"id":"p","name":"Project","color":"`+tc.input+`","connections":[]}`), &project); err != nil {
			t.Fatal(err)
		}
		normalized, err := NormalizeProject(project)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(normalized)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		if fields["color"] != tc.want {
			t.Fatalf("color = %v, want %v", fields["color"], tc.want)
		}
	}
}

func TestProjectColorStoreRoundTrip(t *testing.T) {
	settings := defaultProjectSettings(t)
	settings.Projects[0].Color = "#c06bdf"
	settings.Projects[0].GlowSize = 50
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Projects[0].Color != "#c06bdf" {
		t.Fatalf("lost project color: %+v", loaded.Projects[0])
	}
	if loaded.Projects[0].GlowSize != 50 {
		t.Fatalf("lost glow size: %+v", loaded.Projects[0])
	}
}
