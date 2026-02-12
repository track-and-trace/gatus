package api

import (
	"net/url"
	"strconv"
	"time"

	"github.com/TwiN/gatus/v5/config"
	"github.com/TwiN/gatus/v5/storage/store"
	"github.com/TwiN/gatus/v5/storage/store/common"
	"github.com/TwiN/logr"
	fiber "github.com/gofiber/fiber/v2"
)

type createMaintenanceRequest struct {
	StartTimestamp time.Time `json:"start_timestamp"`
	EndTimestamp   time.Time `json:"end_timestamp"`
	Note           string    `json:"note"`
}

type createMaintenancesByGroupsRequest struct {
	Groups         []string  `json:"groups"`
	StartTimestamp time.Time `json:"start_timestamp"`
	EndTimestamp   time.Time `json:"end_timestamp"`
	Note           string    `json:"note"`
}

type createMaintenancesByKeysRequest struct {
	EndpointKeys   []string  `json:"endpoint_keys"`
	StartTimestamp time.Time `json:"start_timestamp"`
	EndTimestamp   time.Time `json:"end_timestamp"`
	Note           string    `json:"note"`
}

type updateMaintenanceRequest struct {
	EndpointID     int64     `json:"endpoint_id"`
	StartTimestamp time.Time `json:"start_timestamp"`
	EndTimestamp   time.Time `json:"end_timestamp"`
	Note           string    `json:"note"`
}

// GetEndpointMaintenances returns maintenance windows with optional filters
func GetEndpointMaintenances(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		params := &common.EndpointMaintenanceParams{}
		if endpointIDStr := c.Query("endpoint_id"); len(endpointIDStr) > 0 {
			eid, err := strconv.ParseInt(endpointIDStr, 10, 64)
			if err != nil {
				return c.Status(400).SendString("invalid endpoint_id parameter")
			}
			params.EndpointID = eid
		}
		if fromStr := c.Query("from"); len(fromStr) > 0 {
			from, err := time.Parse(time.RFC3339, fromStr)
			if err != nil {
				return c.Status(400).SendString("invalid from parameter, expected RFC3339 format")
			}
			params.From = &from
		}
		if toStr := c.Query("to"); len(toStr) > 0 {
			to, err := time.Parse(time.RFC3339, toStr)
			if err != nil {
				return c.Status(400).SendString("invalid to parameter, expected RFC3339 format")
			}
			params.To = &to
		}
		maintenances, err := store.Get().GetEndpointMaintenances(params)
		if err != nil {
			logr.Errorf("[api.GetEndpointMaintenances] Failed to retrieve maintenances: %s", err.Error())
			return c.Status(500).SendString(err.Error())
		}
		if maintenances == nil {
			maintenances = make([]*common.EndpointMaintenance, 0)
		}
		return c.Status(200).JSON(maintenances)
	}
}

// CreateEndpointMaintenance creates a single maintenance window for an endpoint identified by key
func CreateEndpointMaintenance(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		endpointKey, err := url.QueryUnescape(c.Params("key"))
		if err != nil {
			return c.Status(400).SendString("invalid endpoint key")
		}
		var req createMaintenanceRequest
		if err := c.BodyParser(&req); err != nil {
			return c.Status(400).SendString("invalid request body: " + err.Error())
		}
		if req.StartTimestamp.IsZero() || req.EndTimestamp.IsZero() {
			return c.Status(400).SendString("start_timestamp and end_timestamp are required")
		}
		endpointID, err := store.Get().GetEndpointIDByKey(endpointKey)
		if err != nil {
			logr.Errorf("[api.CreateEndpointMaintenance] Endpoint with key=%s not found: %s", endpointKey, err.Error())
			return c.Status(404).SendString("endpoint not found")
		}
		m := &common.EndpointMaintenance{
			EndpointID:     endpointID,
			StartTimestamp: req.StartTimestamp,
			EndTimestamp:   req.EndTimestamp,
			Note:           req.Note,
		}
		id, err := store.Get().CreateEndpointMaintenance(m)
		if err != nil {
			logr.Errorf("[api.CreateEndpointMaintenance] Failed to create maintenance: %s", err.Error())
			return c.Status(500).SendString(err.Error())
		}
		m.ID = id
		return c.Status(201).JSON(m)
	}
}

// CreateEndpointMaintenancesByGroups creates maintenance windows for all endpoints matching group prefixes
func CreateEndpointMaintenancesByGroups(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var req createMaintenancesByGroupsRequest
		if err := c.BodyParser(&req); err != nil {
			return c.Status(400).SendString("invalid request body: " + err.Error())
		}
		if len(req.Groups) == 0 {
			return c.Status(400).SendString("at least one group prefix is required")
		}
		if req.StartTimestamp.IsZero() || req.EndTimestamp.IsZero() {
			return c.Status(400).SendString("start_timestamp and end_timestamp are required")
		}
		ids, err := store.Get().CreateEndpointMaintenancesByGroups(req.Groups, req.StartTimestamp, req.EndTimestamp, req.Note)
		if err != nil {
			logr.Errorf("[api.CreateEndpointMaintenancesByGroups] Failed to create maintenances: %s", err.Error())
			return c.Status(500).SendString(err.Error())
		}
		return c.Status(201).JSON(fiber.Map{"ids": ids})
	}
}

// CreateEndpointMaintenancesByKeys creates maintenance windows for endpoints identified by their keys
func CreateEndpointMaintenancesByKeys(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var req createMaintenancesByKeysRequest
		if err := c.BodyParser(&req); err != nil {
			return c.Status(400).SendString("invalid request body: " + err.Error())
		}
		if len(req.EndpointKeys) == 0 {
			return c.Status(400).SendString("at least one endpoint key is required")
		}
		if req.StartTimestamp.IsZero() || req.EndTimestamp.IsZero() {
			return c.Status(400).SendString("start_timestamp and end_timestamp are required")
		}
		ids, err := store.Get().CreateEndpointMaintenancesByKeys(req.EndpointKeys, req.StartTimestamp, req.EndTimestamp, req.Note)
		if err != nil {
			logr.Errorf("[api.CreateEndpointMaintenancesByKeys] Failed to create maintenances: %s", err.Error())
			return c.Status(500).SendString(err.Error())
		}
		return c.Status(201).JSON(fiber.Map{"ids": ids})
	}
}

// UpdateEndpointMaintenance updates a maintenance window by its ID
func UpdateEndpointMaintenance(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		maintenanceID, err := strconv.ParseInt(c.Params("id"), 10, 64)
		if err != nil {
			return c.Status(400).SendString("invalid maintenance id")
		}
		var req updateMaintenanceRequest
		if err := c.BodyParser(&req); err != nil {
			return c.Status(400).SendString("invalid request body: " + err.Error())
		}
		if req.EndpointID == 0 {
			return c.Status(400).SendString("endpoint_id is required")
		}
		if req.StartTimestamp.IsZero() || req.EndTimestamp.IsZero() {
			return c.Status(400).SendString("start_timestamp and end_timestamp are required")
		}
		m := &common.EndpointMaintenance{
			ID:             maintenanceID,
			EndpointID:     req.EndpointID,
			StartTimestamp: req.StartTimestamp,
			EndTimestamp:   req.EndTimestamp,
			Note:           req.Note,
		}
		if err := store.Get().UpdateEndpointMaintenance(m); err != nil {
			logr.Errorf("[api.UpdateEndpointMaintenance] Failed to update maintenance id=%d: %s", maintenanceID, err.Error())
			return c.Status(500).SendString(err.Error())
		}
		return c.Status(200).JSON(m)
	}
}

// DeleteEndpointMaintenance deletes a maintenance window by its ID
func DeleteEndpointMaintenance(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		maintenanceID, err := strconv.ParseInt(c.Params("id"), 10, 64)
		if err != nil {
			return c.Status(400).SendString("invalid maintenance id")
		}
		if err := store.Get().DeleteEndpointMaintenance(maintenanceID); err != nil {
			logr.Errorf("[api.DeleteEndpointMaintenance] Failed to delete maintenance id=%d: %s", maintenanceID, err.Error())
			return c.Status(500).SendString(err.Error())
		}
		return c.Status(200).SendString("")
	}
}
