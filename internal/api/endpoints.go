package api

// Endpoint describes a single Oura API v2 endpoint.
type Endpoint struct {
	// Name is the short identifier, e.g. "daily_activity".
	Name string
	// Path is the full API path, e.g. "/v2/usercollection/daily_activity".
	Path string
	// UseDatetime indicates the endpoint uses start_datetime/end_datetime
	// parameters instead of start_date/end_date (only heartrate).
	UseDatetime bool
	// IsSingleton indicates the endpoint returns a single object
	// without pagination (only personal_info).
	IsSingleton bool
	// MaxRangeDays is the maximum date range the API allows per request.
	// 0 means no limit. Ranges exceeding this are split into chunks.
	MaxRangeDays int
}

// Endpoints is the registry of all 18 Oura API v2 endpoints.
var Endpoints = []Endpoint{
	{Name: "personal_info", Path: "/v2/usercollection/personal_info", IsSingleton: true},
	{Name: "daily_activity", Path: "/v2/usercollection/daily_activity"},
	{Name: "daily_readiness", Path: "/v2/usercollection/daily_readiness"},
	{Name: "daily_sleep", Path: "/v2/usercollection/daily_sleep"},
	{Name: "daily_spo2", Path: "/v2/usercollection/daily_spo2"},
	{Name: "daily_stress", Path: "/v2/usercollection/daily_stress"},
	{Name: "daily_cardiovascular_age", Path: "/v2/usercollection/daily_cardiovascular_age"},
	{Name: "daily_resilience", Path: "/v2/usercollection/daily_resilience"},
	{Name: "sleep", Path: "/v2/usercollection/sleep"},
	{Name: "sleep_time", Path: "/v2/usercollection/sleep_time"},
	{Name: "rest_mode_period", Path: "/v2/usercollection/rest_mode_period"},
	{Name: "ring_configuration", Path: "/v2/usercollection/ring_configuration"},
	{Name: "tag", Path: "/v2/usercollection/tag"},
	{Name: "enhanced_tag", Path: "/v2/usercollection/enhanced_tag"},
	{Name: "workout", Path: "/v2/usercollection/workout"},
	{Name: "session", Path: "/v2/usercollection/session"},
	{Name: "vo2_max", Path: "/v2/usercollection/vo2_max"},
	{Name: "heartrate", Path: "/v2/usercollection/heartrate", UseDatetime: true, MaxRangeDays: 30},
}

// EndpointByName returns the endpoint with the given name, or nil if not found.
func EndpointByName(name string) *Endpoint {
	for i := range Endpoints {
		if Endpoints[i].Name == name {
			return &Endpoints[i]
		}
	}
	return nil
}
