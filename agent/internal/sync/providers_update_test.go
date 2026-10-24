package sync

import "testing"

func TestProvidersUpdateRefusesWhatOnlyTheMachineSets(t *testing.T) {
	for _, key := range []string{`"command":"/bin/x"`, `"env":{"A":"b"}`, `"extends":"codex"`, `"configuration":{}`} {
		raw := []byte(`{"v":22,"type":"device.providers.update","requestId":"r","id":"glm",` + key + `}`)
		if _, err := parseProvidersUpdate(raw); err == nil {
			t.Errorf("%s was accepted", key)
		}
	}
	raw := []byte(`{"v":22,"type":"device.providers.update","requestId":"r","id":"glm","name":"G","enabled":false,"defaults":{"model":"m"}}`)
	req, err := parseProvidersUpdate(raw)
	if err != nil {
		t.Fatal(err)
	}
	if req.ID != "glm" || req.RequestID != "r" || req.Name == nil || *req.Name != "G" || req.Enabled == nil || *req.Enabled || req.Defaults["model"] != "m" {
		t.Fatalf("req = %+v", req)
	}
}
