// T31: vision adapter config surface — defaults and fail-closed validation.
package platform

import "testing"

func TestT31VisionDefaults(t *testing.T) {
	c, err := LoadConfig(envOf(map[string]string{
		"DATABASE_URL": "postgres://postgres:devpw@localhost:55432/canopy?sslmode=disable",
	}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.VisionBaseURL != "" || c.VisionAPIKey != "" || c.VisionModel != "" {
		t.Fatal("vision must default to no-provider mode")
	}
	if c.VisionTimeout <= 0 || c.VisionMaxRespBytes < 1024 || c.VisionMaxImageB < 1024 {
		t.Fatalf("vision bounds must have safe defaults: %v %d %d",
			c.VisionTimeout, c.VisionMaxRespBytes, c.VisionMaxImageB)
	}
}

func TestT31VisionValidationMatrix(t *testing.T) {
	base := map[string]string{
		"DATABASE_URL": "postgres://postgres:devpw@localhost:55432/canopy?sslmode=disable",
	}
	cases := []struct {
		name    string
		set     map[string]string
		wantErr bool
	}{
		{"clean origin + explicit model ok", map[string]string{
			"CANOPY_VISION_BASE_URL": "http://192.168.69.130:8080",
			"CANOPY_VISION_MODEL":    "some-vision-model",
		}, false},
		{"https origin + key ok", map[string]string{
			"CANOPY_VISION_BASE_URL": "https://vision.example",
			"CANOPY_VISION_API_KEY":  "k",
			"CANOPY_VISION_MODEL":    "m",
		}, false},
		{"base without model rejected (no default model)", map[string]string{
			"CANOPY_VISION_BASE_URL": "http://vision.example",
		}, true},
		{"pathed origin rejected", map[string]string{
			"CANOPY_VISION_BASE_URL": "http://vision.example/v1", "CANOPY_VISION_MODEL": "m",
		}, true},
		{"credentialed origin rejected", map[string]string{
			"CANOPY_VISION_BASE_URL": "http://u:p@vision.example", "CANOPY_VISION_MODEL": "m",
		}, true},
		{"non-http scheme rejected", map[string]string{
			"CANOPY_VISION_BASE_URL": "ftp://vision.example", "CANOPY_VISION_MODEL": "m",
		}, true},
		{"model without base is inert", map[string]string{
			"CANOPY_VISION_MODEL": "m",
		}, false},
		{"zero timeout rejected", map[string]string{
			"CANOPY_VISION_BASE_URL": "http://vision.example", "CANOPY_VISION_MODEL": "m",
			"CANOPY_VISION_TIMEOUT": "0s",
		}, true},
		{"tiny response bound rejected", map[string]string{
			"CANOPY_VISION_BASE_URL": "http://vision.example", "CANOPY_VISION_MODEL": "m",
			"CANOPY_VISION_MAX_RESPONSE_BYTES": "10",
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set := map[string]string{}
			for k, v := range base {
				set[k] = v
			}
			for k, v := range tc.set {
				set[k] = v
			}
			_, err := LoadConfig(envOf(set))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}
