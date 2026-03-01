package api

import (
	"strings"
	"testing"
)

func TestEndpoints_Count(t *testing.T) {
	if len(Endpoints) != 18 {
		t.Errorf("expected 18 endpoints, got %d", len(Endpoints))
	}
}

func TestEndpoints_UniqueNames(t *testing.T) {
	seen := make(map[string]bool)
	for _, ep := range Endpoints {
		if seen[ep.Name] {
			t.Errorf("duplicate endpoint name: %s", ep.Name)
		}
		seen[ep.Name] = true
	}
}

func TestEndpoints_PathsStartWithPrefix(t *testing.T) {
	prefix := "/v2/usercollection/"
	for _, ep := range Endpoints {
		if !strings.HasPrefix(ep.Path, prefix) {
			t.Errorf("endpoint %s: path %q does not start with %q", ep.Name, ep.Path, prefix)
		}
	}
}

func TestEndpoints_PathContainsName(t *testing.T) {
	for _, ep := range Endpoints {
		if !strings.HasSuffix(ep.Path, "/"+ep.Name) {
			t.Errorf("endpoint %s: path %q does not end with /%s", ep.Name, ep.Path, ep.Name)
		}
	}
}

func TestEndpoints_OnlyHeartrateUsesDatetime(t *testing.T) {
	for _, ep := range Endpoints {
		if ep.UseDatetime && ep.Name != "heartrate" {
			t.Errorf("endpoint %s: UseDatetime should only be true for heartrate", ep.Name)
		}
	}
	hr := EndpointByName("heartrate")
	if hr == nil {
		t.Fatal("heartrate endpoint not found")
	}
	if !hr.UseDatetime {
		t.Error("heartrate endpoint should have UseDatetime=true")
	}
}

func TestEndpoints_OnlyPersonalInfoIsSingleton(t *testing.T) {
	for _, ep := range Endpoints {
		if ep.IsSingleton && ep.Name != "personal_info" {
			t.Errorf("endpoint %s: IsSingleton should only be true for personal_info", ep.Name)
		}
	}
	pi := EndpointByName("personal_info")
	if pi == nil {
		t.Fatal("personal_info endpoint not found")
	}
	if !pi.IsSingleton {
		t.Error("personal_info endpoint should have IsSingleton=true")
	}
}

func TestEndpoints_AllExpectedNamesPresent(t *testing.T) {
	expected := []string{
		"personal_info",
		"daily_activity", "daily_readiness", "daily_sleep", "daily_spo2",
		"daily_stress", "daily_cardiovascular_age", "daily_resilience",
		"sleep", "sleep_time", "rest_mode_period",
		"ring_configuration", "tag", "enhanced_tag",
		"workout", "session", "vo2_max", "heartrate",
	}
	for _, name := range expected {
		if EndpointByName(name) == nil {
			t.Errorf("expected endpoint %q not found in registry", name)
		}
	}
}

func TestEndpointByName_Found(t *testing.T) {
	ep := EndpointByName("daily_activity")
	if ep == nil {
		t.Fatal("expected to find daily_activity endpoint")
	}
	if ep.Path != "/v2/usercollection/daily_activity" {
		t.Errorf("unexpected path: %s", ep.Path)
	}
}

func TestEndpointByName_NotFound(t *testing.T) {
	ep := EndpointByName("nonexistent")
	if ep != nil {
		t.Error("expected nil for nonexistent endpoint")
	}
}
