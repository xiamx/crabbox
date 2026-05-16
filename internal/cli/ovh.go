package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ovh/go-ovh/ovh"
)

// OVHClient wraps the go-ovh SDK for OVH Public Cloud operations.
type OVHClient struct {
	client      *ovh.Client
	serviceName string
	region      string
}

// OVHInstance represents a single OVH Public Cloud instance.
type OVHInstance struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Region      string `json:"region"`
	FlavorID    string `json:"flavorId"`
	ImageID     string `json:"imageId"`
	IPAddresses []struct {
		Version int    `json:"version"`
		IP      string `json:"ip"`
		Type    string `json:"type"`
	} `json:"ipAddresses"`
}

// OVHFlavor represents an OVH server flavor.
type OVHFlavor struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	VCPUs  int    `json:"vCPUs"`
	RAMMB  int    `json:"ram"`
	Region string `json:"region"`
}

// OVHImage represents an OVH system image.
type OVHImage struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Region string `json:"region"`
	Type   string `json:"type"`
}

// NewOVHClient creates a new OVH API client from the configuration.
func NewOVHClient(cfg Config) (*OVHClient, error) {
	if cfg.OVH.ApplicationKey == "" {
		return nil, exit(3, "ovh application key is required (set ovh.applicationKey or CRABBOX_OVH_APPLICATION_KEY)")
	}
	if cfg.OVH.ApplicationSecret == "" {
		return nil, exit(3, "ovh application secret is required (set ovh.applicationSecret or CRABBOX_OVH_APPLICATION_SECRET)")
	}
	if cfg.OVH.ConsumerKey == "" {
		return nil, exit(3, "ovh consumer key is required (set ovh.consumerKey or CRABBOX_OVH_CONSUMER_KEY)")
	}
	if cfg.OVH.ServiceName == "" {
		return nil, exit(3, "ovh service name (project ID) is required (set ovh.serviceName or CRABBOX_OVH_SERVICE_NAME)")
	}
	client, err := ovh.NewClient(
		cfg.OVH.Endpoint,
		cfg.OVH.ApplicationKey,
		cfg.OVH.ApplicationSecret,
		cfg.OVH.ConsumerKey,
	)
	if err != nil {
		return nil, fmt.Errorf("ovh client: %w", err)
	}
	return &OVHClient{client: client, serviceName: cfg.OVH.ServiceName, region: cfg.OVH.Region}, nil
}

// AddSSHKey registers a public SSH key with OVH and returns its ID.
func (c *OVHClient) AddSSHKey(ctx context.Context, name, publicKey, region string) (string, error) {
	var result struct {
		ID        string   `json:"id"`
		Name      string   `json:"name"`
		PublicKey string   `json:"publicKey"`
		Regions   []string `json:"regions"`
	}
	body := map[string]string{
		"name":      name,
		"publicKey": publicKey,
		"region":    region,
	}
	path := fmt.Sprintf("/cloud/project/%s/sshkey", c.serviceName)
	if err := c.client.PostWithContext(ctx, path, body, &result); err != nil {
		return "", fmt.Errorf("ovh add ssh key: %w", err)
	}
	return result.ID, nil
}

// DeleteSSHKey removes an SSH key by its OVH ID.
func (c *OVHClient) DeleteSSHKey(ctx context.Context, keyID string) error {
	path := fmt.Sprintf("/cloud/project/%s/sshkey/%s", c.serviceName, keyID)
	if err := c.client.DeleteWithContext(ctx, path, nil); err != nil {
		return fmt.Errorf("ovh delete ssh key: %w", err)
	}
	return nil
}

// CreateInstance provisions a new OVH instance and returns its ID.
func (c *OVHClient) CreateInstance(ctx context.Context, name, flavorID, imageID, sshKeyID, region string) (string, error) {
	body := map[string]interface{}{
		"flavorId":       flavorID,
		"imageId":        imageID,
		"name":           name,
		"region":         region,
		"monthlyBilling": false,
		"sshKeyId":       sshKeyID,
	}
	var result OVHInstance
	path := fmt.Sprintf("/cloud/project/%s/instance", c.serviceName)
	if err := c.client.PostWithContext(ctx, path, body, &result); err != nil {
		return "", fmt.Errorf("ovh create instance: %w", err)
	}
	return result.ID, nil
}

// GetInstance retrieves a single OVH instance by ID.
func (c *OVHClient) GetInstance(ctx context.Context, instanceID string) (*OVHInstance, error) {
	var result OVHInstance
	path := fmt.Sprintf("/cloud/project/%s/instance/%s", c.serviceName, instanceID)
	if err := c.client.GetWithContext(ctx, path, &result); err != nil {
		return nil, fmt.Errorf("ovh get instance: %w", err)
	}
	return &result, nil
}

// DeleteInstance terminates an OVH instance. 404s are silently accepted.
func (c *OVHClient) DeleteInstance(ctx context.Context, instanceID string) error {
	path := fmt.Sprintf("/cloud/project/%s/instance/%s", c.serviceName, instanceID)
	if err := c.client.DeleteWithContext(ctx, path, nil); err != nil {
		if isOVHNotFound(err) {
			return nil
		}
		return fmt.Errorf("ovh delete instance: %w", err)
	}
	return nil
}

// ListFlavors returns all available flavors in a given region.
func (c *OVHClient) ListFlavors(ctx context.Context, region string) ([]OVHFlavor, error) {
	var ids []string
	path := fmt.Sprintf("/cloud/project/%s/flavor", c.serviceName)
	params := fmt.Sprintf("?region=%s", region)
	if err := c.client.GetWithContext(ctx, path+params, &ids); err != nil {
		return nil, fmt.Errorf("ovh list flavors: %w", err)
	}
	var flavors []OVHFlavor
	for _, id := range ids {
		var f OVHFlavor
		flavorPath := fmt.Sprintf("/cloud/project/%s/flavor/%s", c.serviceName, id)
		if err := c.client.GetWithContext(ctx, flavorPath, &f); err != nil {
			continue
		}
		flavors = append(flavors, f)
	}
	return flavors, nil
}

// ListImages returns all available images in a given region.
func (c *OVHClient) ListImages(ctx context.Context, region string) ([]OVHImage, error) {
	var ids []string
	path := fmt.Sprintf("/cloud/project/%s/image", c.serviceName)
	params := fmt.Sprintf("?region=%s", region)
	if err := c.client.GetWithContext(ctx, path+params, &ids); err != nil {
		return nil, fmt.Errorf("ovh list images: %w", err)
	}
	var images []OVHImage
	for _, id := range ids {
		var img OVHImage
		imagePath := fmt.Sprintf("/cloud/project/%s/image/%s", c.serviceName, id)
		if err := c.client.GetWithContext(ctx, imagePath, &img); err != nil {
			continue
		}
		images = append(images, img)
	}
	return images, nil
}

// ListInstances returns all instances in the OVH project.
func (c *OVHClient) ListInstances(ctx context.Context) ([]OVHInstance, error) {
	var ids []string
	path := fmt.Sprintf("/cloud/project/%s/instance", c.serviceName)
	if err := c.client.GetWithContext(ctx, path, &ids); err != nil {
		return nil, fmt.Errorf("ovh list instances: %w", err)
	}
	var instances []OVHInstance
	for _, id := range ids {
		var inst OVHInstance
		instPath := fmt.Sprintf("/cloud/project/%s/instance/%s", c.serviceName, id)
		if err := c.client.GetWithContext(ctx, instPath, &inst); err != nil {
			continue
		}
		instances = append(instances, inst)
	}
	return instances, nil
}

// PublicIPv4 returns the first public IPv4 address for the instance.
func (inst *OVHInstance) PublicIPv4() string {
	for _, addr := range inst.IPAddresses {
		if addr.Version == 4 && addr.Type == "public" {
			return addr.IP
		}
	}
	return ""
}

// WaitForInstanceIP polls an instance until it acquires a public IPv4 address.
func (c *OVHClient) WaitForInstanceIP(ctx context.Context, instanceID string) (string, error) {
	deadline := time.Now().Add(2 * time.Minute)
	for {
		inst, err := c.GetInstance(ctx, instanceID)
		if err != nil {
			return "", err
		}
		if ip := inst.PublicIPv4(); ip != "" {
			return ip, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timeout waiting for ovh public ip on %s", instanceID)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// SetLabels is a no-op since OVH instances don't support arbitrary labels.
func (c *OVHClient) SetLabels(ctx context.Context, instanceID string, labels map[string]string) error {
	return nil
}

// isOVHNotFound checks if an error indicates a 404 or "not found" response.
func isOVHNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found")
}
