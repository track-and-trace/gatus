package common

import "time"

// EndpointMaintenance represents a scheduled maintenance window for an endpoint.
// During a maintenance window, alerts are recorded in the DB but not sent via providers.
type EndpointMaintenance struct {
	ID             int64     `json:"id"`
	EndpointID     int64     `json:"endpoint_id"`
	StartTimestamp time.Time `json:"start_timestamp"`
	EndTimestamp   time.Time `json:"end_timestamp"`
	Note           string    `json:"note"`
}

// EndpointMaintenanceParams defines filters for querying maintenance windows.
type EndpointMaintenanceParams struct {
	EndpointID int64      // filter by endpoint ID (0 = all)
	From       *time.Time // filter: start_timestamp >= From
	To         *time.Time // filter: end_timestamp <= To
}
