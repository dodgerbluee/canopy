// T30: PlantNet + OCR config surface — defaults and fail-closed validation.
package platform

import "testing"

func TestT30AdapterDefaults(t *testing.T) {
	c, err := LoadConfig(envOf(map[string]string{
		"DATABASE_URL": "postgres://postgres:devpw@localhost:55432/canopy?sslmode=disable",
	}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.PlantNetAPIKey != "" {
		t.Fatal("identification must default to no-provider")
	}
	if c.PlantNetBaseURL != "https://my-api.plantnet.org" {
		t.Fatalf("plantnet origin default: %q", c.PlantNetBaseURL)
	}
	if c.OCRLangs != "eng" || c.OCRMaxPages != 3 {
		t.Fatalf("ocr defaults: %q %d", c.OCRLangs, c.OCRMaxPages)
	}
}

func TestT30AdapterValidationMatrix(t *testing.T) {
	base := map[string]string{
		"DATABASE_URL": "postgres://postgres:devpw@localhost:55432/canopy?sslmode=disable",
	}
	cases := []struct {
		name    string
		set     map[string]string
		wantErr bool
	}{
		{"key with clean origin ok", map[string]string{
			"CANOPY_PLANTNET_API_KEY": "k", "CANOPY_PLANTNET_BASE_URL": "https://my-api.plantnet.org",
		}, false},
		{"key with pathed origin rejected", map[string]string{
			"CANOPY_PLANTNET_API_KEY": "k", "CANOPY_PLANTNET_BASE_URL": "https://x.example/api/v2",
		}, true},
		{"key with credentialed origin rejected", map[string]string{
			"CANOPY_PLANTNET_API_KEY": "k", "CANOPY_PLANTNET_BASE_URL": "https://u:p@x.example",
		}, true},
		{"langs eng+deu ok", map[string]string{"CANOPY_OCR_LANGS": "eng+deu"}, false},
		{"tesseract script token ok", map[string]string{"CANOPY_OCR_LANGS": "chi_sim"}, false},
		{"shell-injected langs rejected", map[string]string{"CANOPY_OCR_LANGS": "eng; rm -rf /"}, true},
		{"empty langs rejected", map[string]string{"CANOPY_OCR_LANGS": ""}, true},
		{"max pages 0 rejected", map[string]string{"CANOPY_OCR_MAX_PAGES": "0"}, true},
		{"max pages 11 rejected", map[string]string{"CANOPY_OCR_MAX_PAGES": "11"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{}
			for k, v := range base {
				env[k] = v
			}
			for k, v := range tc.set {
				env[k] = v
			}
			_, err := LoadConfig(envOf(env))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestT30ProdRequiresHTTPS(t *testing.T) {
	_, err := LoadConfig(envOf(map[string]string{
		"CANOPY_ENV":               "prod",
		"DATABASE_URL":             "postgres://app:secret@db.internal:5432/canopy?sslmode=require",
		"CANOPY_STORAGE_ROOT":      "/var/lib/canopy/media",
		"CANOPY_PLANTNET_API_KEY":  "k",
		"CANOPY_PLANTNET_BASE_URL": "http://my-api.plantnet.org",
	}))
	if err == nil {
		t.Fatal("prod plantnet http origin must stop startup")
	}
}
