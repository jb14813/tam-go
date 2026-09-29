package remote

import (
	"encoding/json"
	"testing"
)

func TestNativeCapabilityRequiresEverySupportedField(t *testing.T) {
	valid := map[string]any{"whoami": "TAM Server", "healthy": true, "authenticated": true, "backup_metadata": true, "receipts": true, "conflicts": 0, "review_token": ""}
	for key := range valid {
		t.Run(key, func(t *testing.T) {
			broken := map[string]any{}
			for field, value := range valid {
				if field != key {
					broken[field] = value
				}
			}
			body, _ := json.Marshal(broken)
			if _, err := (&Response{Status: 200, Body: body}).NativeStatus(); err == nil {
				t.Fatalf("missing %s accepted", key)
			}
		})
	}
	body, _ := json.Marshal(valid)
	if _, err := (&Response{Status: 200, Body: body}).NativeStatus(); err != nil {
		t.Fatal(err)
	}
}
