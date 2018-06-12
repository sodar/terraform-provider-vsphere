package vappcontainer

import (
	"context"
	"fmt"
	"log"

	"github.com/terraform-providers/terraform-provider-vsphere/vsphere/internal/helper/provider"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// FromPath returns a VirtualApp via its supplied path.
func FromPath(client *govmomi.Client, name string, dc *object.Datacenter) (*object.VirtualApp, error) {
	finder := find.NewFinder(client.Client, false)

	ctx, cancel := context.WithTimeout(context.Background(), provider.DefaultAPITimeout)
	defer cancel()
	if dc != nil {
		finder.SetDatacenter(dc)
	}
	return finder.VirtualApp(ctx, name)
}

// FromID locates a VirtualApp by its managed object reference ID.
func FromID(client *govmomi.Client, id string) (*object.VirtualApp, error) {
	log.Printf("[DEBUG] Locating resource pool with ID %s", id)
	finder := find.NewFinder(client.Client, false)

	ref := types.ManagedObjectReference{
		Type:  "VirtualApp",
		Value: id,
	}

	ctx, cancel := context.WithTimeout(context.Background(), provider.DefaultAPITimeout)
	defer cancel()
	obj, err := finder.ObjectReference(ctx, ref)
	if err != nil {
		return nil, err
	}
	log.Printf("[DEBUG] vApp container found: %s", obj.Reference().Value)
	return obj.(*object.VirtualApp), nil
}

func IsVApp(client *govmomi.Client, rp string) bool {
	_, err := FromID(client, rp)
	if err != nil {
		return false
	}
	return true
}

// EntityFromKey locates a VAppEntityConfigInfo within a vApp container by the
// string value of its key.
func EntityFromKey(key string, c *mo.VirtualApp) *types.VAppEntityConfigInfo {
	log.Printf("[DEBUG] Locating VApp entity with key %s", key)
	for _, e := range c.VAppConfig.EntityConfig {
		log.Printf("[DEBUG] BILLLLLLLLLLLLLLLLLLLLLLLL - %v, %v", e.Key.Value, key)
		if e.Key.Value == key {
			log.Printf("[DEBUG] vApp entity found: %s", key)
			return &e
		}
	}
	return nil
}

// Properties returns the VirtualApp managed object from its higher-level
// object.
func Properties(obj *object.VirtualApp) (*mo.VirtualApp, error) {
	ctx, cancel := context.WithTimeout(context.Background(), provider.DefaultAPITimeout)
	defer cancel()
	var props mo.VirtualApp
	if err := obj.Properties(ctx, obj.Reference(), nil, &props); err != nil {
		return nil, err
	}
	return &props, nil
}

// Create creates a VirtualApp.
func Create(va *object.ResourcePool, name string, resSpec *types.ResourceConfigSpec, vSpec *types.VAppConfigSpec, folder *object.Folder) (*object.VirtualApp, error) {
	log.Printf("[DEBUG] Creating vApp container %q", fmt.Sprintf("%s/%s", va.InventoryPath, name))
	ctx, cancel := context.WithTimeout(context.Background(), provider.DefaultAPITimeout)
	defer cancel()
	nva, err := va.CreateVApp(ctx, name, *resSpec, *vSpec, folder)
	if err != nil {
		return nil, err
	}
	return nva, nil
}

// Update updates a VirtualApp.
func Update(va *object.VirtualApp, spec types.VAppConfigSpec) error {
	log.Printf("[DEBUG] Updating vApp container %q", fmt.Sprintf("%s", va.InventoryPath))
	ctx, cancel := context.WithTimeout(context.Background(), provider.DefaultAPITimeout)
	defer cancel()
	return va.UpdateConfig(ctx, spec)
}

// Delete destroys a VirtualApp.
func Delete(va *object.VirtualApp) error {
	log.Printf("[DEBUG] Deleting vApp container %q", va.InventoryPath)
	ctx, cancel := context.WithTimeout(context.Background(), provider.DefaultAPITimeout)
	defer cancel()
	task, err := va.Destroy(ctx)
	if err != nil {
		return err
	}
	return task.Wait(ctx)
}

// HasChildren checks to see if a vApp container has any child items (virtual
// machines, vApps, or resource pools) and returns true if that is the case.
// This is useful when checking to see if a vApp container is safe to delete.
// Destroying a vApp container in vSphere destroys *all* children if at all
// possible, so extra verification is necessary to prevent accidental removal.
func HasChildren(va *object.VirtualApp) (bool, error) {
	props, err := Properties(va)
	if err != nil {
		return false, err
	}
	if len(props.Vm) > 0 || len(props.ResourcePool.ResourcePool) > 0 {
		return true, nil
	}
	return false, nil
}
