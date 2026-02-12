package memory

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TwiN/gatus/v5/alerting/alert"
	"github.com/TwiN/gatus/v5/config/endpoint"
	"github.com/TwiN/gatus/v5/config/key"
	"github.com/TwiN/gatus/v5/config/suite"
	"github.com/TwiN/gatus/v5/storage/store/common"
	"github.com/TwiN/gatus/v5/storage/store/common/paging"
	"github.com/TwiN/gocache/v2"
	"github.com/TwiN/logr"
)

// Store that leverages gocache
type Store struct {
	sync.RWMutex

	endpointCache *gocache.Cache // Cache for endpoint statuses
	suiteCache    *gocache.Cache // Cache for suite statuses

	maintenances         []*common.EndpointMaintenance // In-memory maintenance windows
	maintenanceIDCounter int64                         // Auto-incrementing ID for maintenances

	maximumNumberOfResults int // maximum number of results that an endpoint can have
	maximumNumberOfEvents  int // maximum number of events that an endpoint can have
}

// NewStore creates a new store using gocache.Cache
//
// This store holds everything in memory, and if the file parameter is not blank,
// supports eventual persistence.
func NewStore(maximumNumberOfResults, maximumNumberOfEvents int) (*Store, error) {
	store := &Store{
		endpointCache:          gocache.NewCache().WithMaxSize(gocache.NoMaxSize),
		suiteCache:             gocache.NewCache().WithMaxSize(gocache.NoMaxSize),
		maximumNumberOfResults: maximumNumberOfResults,
		maximumNumberOfEvents:  maximumNumberOfEvents,
	}
	return store, nil
}

// GetAllEndpointStatuses returns all monitored endpoint.Status
// with a subset of endpoint.Result defined by the page and pageSize parameters
func (s *Store) GetAllEndpointStatuses(params *paging.EndpointStatusParams) ([]*endpoint.Status, error) {
	s.RLock()
	defer s.RUnlock()
	allStatuses := s.endpointCache.GetAll()
	pagedEndpointStatuses := make([]*endpoint.Status, 0, len(allStatuses))
	for _, v := range allStatuses {
		if status, ok := v.(*endpoint.Status); ok {
			pagedEndpointStatuses = append(pagedEndpointStatuses, ShallowCopyEndpointStatus(status, params))
		}
	}
	sort.Slice(pagedEndpointStatuses, func(i, j int) bool {
		return pagedEndpointStatuses[i].Key < pagedEndpointStatuses[j].Key
	})
	return pagedEndpointStatuses, nil
}

// GetAllSuiteStatuses returns all monitored suite.Status
func (s *Store) GetAllSuiteStatuses(params *paging.SuiteStatusParams) ([]*suite.Status, error) {
	s.RLock()
	defer s.RUnlock()
	suiteStatuses := make([]*suite.Status, 0)
	for _, v := range s.suiteCache.GetAll() {
		if status, ok := v.(*suite.Status); ok {
			suiteStatuses = append(suiteStatuses, ShallowCopySuiteStatus(status, params))
		}
	}
	sort.Slice(suiteStatuses, func(i, j int) bool {
		return suiteStatuses[i].Key < suiteStatuses[j].Key
	})
	return suiteStatuses, nil
}

// GetEndpointStatus returns the endpoint status for a given endpoint name in the given group
func (s *Store) GetEndpointStatus(groupName, endpointName string, params *paging.EndpointStatusParams) (*endpoint.Status, error) {
	return s.GetEndpointStatusByKey(key.ConvertGroupAndNameToKey(groupName, endpointName), params)
}

// GetEndpointStatusByKey returns the endpoint status for a given key
func (s *Store) GetEndpointStatusByKey(key string, params *paging.EndpointStatusParams) (*endpoint.Status, error) {
	s.RLock()
	defer s.RUnlock()
	endpointStatus := s.endpointCache.GetValue(key)
	if endpointStatus == nil {
		return nil, common.ErrEndpointNotFound
	}
	return ShallowCopyEndpointStatus(endpointStatus.(*endpoint.Status), params), nil
}

// GetSuiteStatusByKey returns the suite status for a given key
func (s *Store) GetSuiteStatusByKey(key string, params *paging.SuiteStatusParams) (*suite.Status, error) {
	s.RLock()
	defer s.RUnlock()
	suiteStatus := s.suiteCache.GetValue(key)
	if suiteStatus == nil {
		return nil, common.ErrSuiteNotFound
	}
	return ShallowCopySuiteStatus(suiteStatus.(*suite.Status), params), nil
}

// GetUptimeByKey returns the uptime percentage during a time range
func (s *Store) GetUptimeByKey(key string, from, to time.Time) (float64, error) {
	if from.After(to) {
		return 0, common.ErrInvalidTimeRange
	}
	s.RLock()
	defer s.RUnlock()
	endpointStatus := s.endpointCache.GetValue(key)
	if endpointStatus == nil || endpointStatus.(*endpoint.Status).Uptime == nil {
		return 0, common.ErrEndpointNotFound
	}
	successfulExecutions := uint64(0)
	totalExecutions := uint64(0)
	current := from
	for to.Sub(current) >= 0 {
		hourlyUnixTimestamp := current.Truncate(time.Hour).Unix()
		hourlyStats := endpointStatus.(*endpoint.Status).Uptime.HourlyStatistics[hourlyUnixTimestamp]
		if hourlyStats == nil || hourlyStats.TotalExecutions == 0 {
			current = current.Add(time.Hour)
			continue
		}
		successfulExecutions += hourlyStats.SuccessfulExecutions
		totalExecutions += hourlyStats.TotalExecutions
		current = current.Add(time.Hour)
	}
	if totalExecutions == 0 {
		return 0, nil
	}
	return float64(successfulExecutions) / float64(totalExecutions), nil
}

// GetAverageResponseTimeByKey returns the average response time in milliseconds (value) during a time range
func (s *Store) GetAverageResponseTimeByKey(key string, from, to time.Time) (int, error) {
	if from.After(to) {
		return 0, common.ErrInvalidTimeRange
	}
	s.RLock()
	defer s.RUnlock()
	endpointStatus := s.endpointCache.GetValue(key)
	if endpointStatus == nil || endpointStatus.(*endpoint.Status).Uptime == nil {
		return 0, common.ErrEndpointNotFound
	}
	current := from
	var totalExecutions, totalResponseTime uint64
	for to.Sub(current) >= 0 {
		hourlyUnixTimestamp := current.Truncate(time.Hour).Unix()
		hourlyStats := endpointStatus.(*endpoint.Status).Uptime.HourlyStatistics[hourlyUnixTimestamp]
		if hourlyStats == nil || hourlyStats.TotalExecutions == 0 {
			current = current.Add(time.Hour)
			continue
		}
		totalExecutions += hourlyStats.TotalExecutions
		totalResponseTime += hourlyStats.TotalExecutionsResponseTime
		current = current.Add(time.Hour)
	}
	if totalExecutions == 0 {
		return 0, nil
	}
	return int(float64(totalResponseTime) / float64(totalExecutions)), nil
}

// GetHourlyAverageResponseTimeByKey returns a map of hourly (key) average response time in milliseconds (value) during a time range
func (s *Store) GetHourlyAverageResponseTimeByKey(key string, from, to time.Time) (map[int64]int, error) {
	if from.After(to) {
		return nil, common.ErrInvalidTimeRange
	}
	s.RLock()
	defer s.RUnlock()
	endpointStatus := s.endpointCache.GetValue(key)
	if endpointStatus == nil || endpointStatus.(*endpoint.Status).Uptime == nil {
		return nil, common.ErrEndpointNotFound
	}
	hourlyAverageResponseTimes := make(map[int64]int)
	current := from
	for to.Sub(current) >= 0 {
		hourlyUnixTimestamp := current.Truncate(time.Hour).Unix()
		hourlyStats := endpointStatus.(*endpoint.Status).Uptime.HourlyStatistics[hourlyUnixTimestamp]
		if hourlyStats == nil || hourlyStats.TotalExecutions == 0 {
			current = current.Add(time.Hour)
			continue
		}
		hourlyAverageResponseTimes[hourlyUnixTimestamp] = int(float64(hourlyStats.TotalExecutionsResponseTime) / float64(hourlyStats.TotalExecutions))
		current = current.Add(time.Hour)
	}
	return hourlyAverageResponseTimes, nil
}

// InsertEndpointResult adds the observed result for the specified endpoint into the store
func (s *Store) InsertEndpointResult(ep *endpoint.Endpoint, result *endpoint.Result) error {
	endpointKey := ep.Key()
	s.Lock()
	status, exists := s.endpointCache.Get(endpointKey)
	if !exists {
		status = endpoint.NewStatus(ep.Group, ep.Name)
		status.(*endpoint.Status).Events = append(status.(*endpoint.Status).Events, &endpoint.Event{
			Type:      endpoint.EventStart,
			Timestamp: time.Now(),
		})
	}
	AddResult(status.(*endpoint.Status), result, s.maximumNumberOfResults, s.maximumNumberOfEvents)
	s.endpointCache.Set(endpointKey, status)
	s.Unlock()
	return nil
}

// InsertSuiteResult adds the observed result for the specified suite into the store
func (s *Store) InsertSuiteResult(su *suite.Suite, result *suite.Result) error {
	s.Lock()
	defer s.Unlock()
	suiteKey := su.Key()
	suiteStatus := s.suiteCache.GetValue(suiteKey)
	if suiteStatus == nil {
		suiteStatus = &suite.Status{
			Name:    su.Name,
			Group:   su.Group,
			Key:     su.Key(),
			Results: []*suite.Result{},
		}
		logr.Debugf("[memory.InsertSuiteResult] Created new suite status for suiteKey=%s", suiteKey)
	}
	status := suiteStatus.(*suite.Status)
	// Add the new result at the end (append like endpoint implementation)
	status.Results = append(status.Results, result)
	// Keep only the maximum number of results
	if len(status.Results) > s.maximumNumberOfResults {
		status.Results = status.Results[len(status.Results)-s.maximumNumberOfResults:]
	}
	s.suiteCache.Set(suiteKey, status)
	logr.Debugf("[memory.InsertSuiteResult] Stored suite result for suiteKey=%s, total results=%d", suiteKey, len(status.Results))
	return nil
}

// DeleteAllEndpointStatusesNotInKeys removes all Status that are not within the keys provided
func (s *Store) DeleteAllEndpointStatusesNotInKeys(keys []string) int {
	var keysToDelete []string
	for _, existingKey := range s.endpointCache.GetKeysByPattern("*", 0) {
		shouldDelete := !slices.Contains(keys, existingKey)
		if shouldDelete {
			keysToDelete = append(keysToDelete, existingKey)
		}
	}
	return s.endpointCache.DeleteAll(keysToDelete)
}

// DeleteAllSuiteStatusesNotInKeys removes all suite statuses that are not within the keys provided
func (s *Store) DeleteAllSuiteStatusesNotInKeys(keys []string) int {
	s.Lock()
	defer s.Unlock()
	keysToKeep := make(map[string]bool, len(keys))
	for _, k := range keys {
		keysToKeep[k] = true
	}
	var keysToDelete []string
	for existingKey := range s.suiteCache.GetAll() {
		if !keysToKeep[existingKey] {
			keysToDelete = append(keysToDelete, existingKey)
		}
	}
	return s.suiteCache.DeleteAll(keysToDelete)
}

// GetTriggeredEndpointAlert returns whether the triggered alert for the specified endpoint as well as the necessary information to resolve it
//
// Always returns that the alert does not exist for the in-memory store since it does not support persistence across restarts
func (s *Store) GetTriggeredEndpointAlert(ep *endpoint.Endpoint, alert *alert.Alert) (exists bool, resolveKey string, numberOfSuccessesInARow int, err error) {
	return false, "", 0, nil
}

// UpsertTriggeredEndpointAlert inserts/updates a triggered alert for an endpoint
// Used for persistence of triggered alerts across application restarts
//
// Does nothing for the in-memory store since it does not support persistence across restarts
func (s *Store) UpsertTriggeredEndpointAlert(ep *endpoint.Endpoint, triggeredAlert *alert.Alert) error {
	return nil
}

// DeleteTriggeredEndpointAlert deletes a triggered alert for an endpoint
//
// Does nothing for the in-memory store since it does not support persistence across restarts
func (s *Store) DeleteTriggeredEndpointAlert(ep *endpoint.Endpoint, triggeredAlert *alert.Alert) error {
	return nil
}

// DeleteAllTriggeredAlertsNotInChecksumsByEndpoint removes all triggered alerts owned by an endpoint whose alert
// configurations are not provided in the checksums list.
// This prevents triggered alerts that have been removed or modified from lingering in the database.
//
// Does nothing for the in-memory store since it does not support persistence across restarts
func (s *Store) DeleteAllTriggeredAlertsNotInChecksumsByEndpoint(ep *endpoint.Endpoint, checksums []string) int {
	return 0
}

// HasEndpointStatusNewerThan checks whether an endpoint has a status newer than the provided timestamp
func (s *Store) HasEndpointStatusNewerThan(key string, timestamp time.Time) (bool, error) {
	s.RLock()
	defer s.RUnlock()
	endpointStatus := s.endpointCache.GetValue(key)
	if endpointStatus == nil {
		// If no endpoint exists, there's no newer status, so return false instead of an error
		return false, nil
	}
	status, ok := endpointStatus.(*endpoint.Status)
	if !ok {
		return false, nil
	}
	for _, result := range status.Results {
		if result.Timestamp.After(timestamp) {
			return true, nil
		}
	}
	return false, nil
}

// GetEndpointIDByKey returns a synthetic endpoint ID for a given key in the memory store.
// Uses the cache key index as a stable identifier.
func (s *Store) GetEndpointIDByKey(k string) (int64, error) {
	s.RLock()
	defer s.RUnlock()
	endpointStatus := s.endpointCache.GetValue(k)
	if endpointStatus == nil {
		return 0, common.ErrEndpointNotFound
	}
	// Use a hash-based synthetic ID from the key's position in sorted keys
	allKeys := s.endpointCache.GetKeysByPattern("*", 0)
	sort.Strings(allKeys)
	for i, existingKey := range allKeys {
		if existingKey == k {
			return int64(i + 1), nil
		}
	}
	return 0, common.ErrEndpointNotFound
}

// IsEndpointInMaintenance checks if an endpoint has an active maintenance window at the given time
func (s *Store) IsEndpointInMaintenance(endpointID int64, at time.Time) (bool, error) {
	s.RLock()
	defer s.RUnlock()
	for _, m := range s.maintenances {
		if m.EndpointID == endpointID && !at.Before(m.StartTimestamp) && at.Before(m.EndTimestamp) {
			return true, nil
		}
	}
	return false, nil
}

// CreateEndpointMaintenance creates a maintenance window and returns its auto-generated ID
func (s *Store) CreateEndpointMaintenance(m *common.EndpointMaintenance) (int64, error) {
	if !m.EndTimestamp.After(m.StartTimestamp) {
		return 0, fmt.Errorf("end_timestamp must be after start_timestamp")
	}
	s.Lock()
	defer s.Unlock()
	s.maintenanceIDCounter++
	newMaintenance := &common.EndpointMaintenance{
		ID:             s.maintenanceIDCounter,
		EndpointID:     m.EndpointID,
		StartTimestamp: m.StartTimestamp,
		EndTimestamp:   m.EndTimestamp,
		Note:           m.Note,
	}
	s.maintenances = append(s.maintenances, newMaintenance)
	return newMaintenance.ID, nil
}

// CreateEndpointMaintenancesByGroups creates maintenance windows for all endpoints whose group
// starts with any of the given group prefixes
func (s *Store) CreateEndpointMaintenancesByGroups(groupPrefixes []string, start, end time.Time, note string) ([]int64, error) {
	if !end.After(start) {
		return nil, fmt.Errorf("end_timestamp must be after start_timestamp")
	}
	s.Lock()
	defer s.Unlock()
	var ids []int64
	allStatuses := s.endpointCache.GetAll()
	for _, v := range allStatuses {
		status, ok := v.(*endpoint.Status)
		if !ok {
			continue
		}
		for _, prefix := range groupPrefixes {
			if strings.HasPrefix(status.Group, prefix) {
				// Find the endpoint ID
				epKey := key.ConvertGroupAndNameToKey(status.Group, status.Name)
				allKeys := s.endpointCache.GetKeysByPattern("*", 0)
				sort.Strings(allKeys)
				var eid int64
				for i, k := range allKeys {
					if k == epKey {
						eid = int64(i + 1)
						break
					}
				}
				if eid == 0 {
					continue
				}
				s.maintenanceIDCounter++
				s.maintenances = append(s.maintenances, &common.EndpointMaintenance{
					ID:             s.maintenanceIDCounter,
					EndpointID:     eid,
					StartTimestamp: start,
					EndTimestamp:   end,
					Note:           note,
				})
				ids = append(ids, s.maintenanceIDCounter)
				break
			}
		}
	}
	return ids, nil
}

// CreateEndpointMaintenancesByKeys creates maintenance windows for endpoints identified by their keys
func (s *Store) CreateEndpointMaintenancesByKeys(endpointKeys []string, start, end time.Time, note string) ([]int64, error) {
	if !end.After(start) {
		return nil, fmt.Errorf("end_timestamp must be after start_timestamp")
	}
	s.Lock()
	defer s.Unlock()
	allKeys := s.endpointCache.GetKeysByPattern("*", 0)
	sort.Strings(allKeys)
	var ids []int64
	for _, epKey := range endpointKeys {
		var eid int64
		for i, k := range allKeys {
			if k == epKey {
				eid = int64(i + 1)
				break
			}
		}
		if eid == 0 {
			return nil, fmt.Errorf("endpoint with key=%s not found", epKey)
		}
		s.maintenanceIDCounter++
		s.maintenances = append(s.maintenances, &common.EndpointMaintenance{
			ID:             s.maintenanceIDCounter,
			EndpointID:     eid,
			StartTimestamp: start,
			EndTimestamp:   end,
			Note:           note,
		})
		ids = append(ids, s.maintenanceIDCounter)
	}
	return ids, nil
}

// UpdateEndpointMaintenance updates a maintenance window identified by its ID
func (s *Store) UpdateEndpointMaintenance(m *common.EndpointMaintenance) error {
	if !m.EndTimestamp.After(m.StartTimestamp) {
		return fmt.Errorf("end_timestamp must be after start_timestamp")
	}
	s.Lock()
	defer s.Unlock()
	for _, existing := range s.maintenances {
		if existing.ID == m.ID {
			existing.EndpointID = m.EndpointID
			existing.StartTimestamp = m.StartTimestamp
			existing.EndTimestamp = m.EndTimestamp
			existing.Note = m.Note
			return nil
		}
	}
	return fmt.Errorf("maintenance with id=%d not found", m.ID)
}

// DeleteEndpointMaintenance deletes a maintenance window by its ID
func (s *Store) DeleteEndpointMaintenance(maintenanceID int64) error {
	s.Lock()
	defer s.Unlock()
	for i, m := range s.maintenances {
		if m.ID == maintenanceID {
			s.maintenances = append(s.maintenances[:i], s.maintenances[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("maintenance with id=%d not found", maintenanceID)
}

// GetEndpointMaintenances lists maintenance windows with optional filters
func (s *Store) GetEndpointMaintenances(params *common.EndpointMaintenanceParams) ([]*common.EndpointMaintenance, error) {
	s.RLock()
	defer s.RUnlock()
	var result []*common.EndpointMaintenance
	for _, m := range s.maintenances {
		if params.EndpointID != 0 && m.EndpointID != params.EndpointID {
			continue
		}
		if params.From != nil && m.StartTimestamp.Before(*params.From) {
			continue
		}
		if params.To != nil && m.EndTimestamp.After(*params.To) {
			continue
		}
		result = append(result, &common.EndpointMaintenance{
			ID:             m.ID,
			EndpointID:     m.EndpointID,
			StartTimestamp: m.StartTimestamp,
			EndTimestamp:   m.EndTimestamp,
			Note:           m.Note,
		})
	}
	return result, nil
}

// Clear deletes everything from the store
func (s *Store) Clear() {
	s.endpointCache.Clear()
	s.suiteCache.Clear()
	s.maintenances = nil
	s.maintenanceIDCounter = 0
}

// Save persists the cache to the store file
func (s *Store) Save() error {
	return nil
}

// Close does nothing, because there's nothing to close
func (s *Store) Close() {
	return
}
