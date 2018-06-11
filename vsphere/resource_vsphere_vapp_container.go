package vsphere

import (
	"fmt"
	"log"
	"strings"

	"github.com/hashicorp/terraform/helper/schema"
	"github.com/hashicorp/terraform/helper/validation"
	"github.com/terraform-providers/terraform-provider-vsphere/vsphere/internal/helper/customattribute"
	"github.com/terraform-providers/terraform-provider-vsphere/vsphere/internal/helper/folder"
	"github.com/terraform-providers/terraform-provider-vsphere/vsphere/internal/helper/resourcepool"
	"github.com/terraform-providers/terraform-provider-vsphere/vsphere/internal/helper/structure"
	"github.com/terraform-providers/terraform-provider-vsphere/vsphere/internal/helper/vappcontainer"
	"github.com/terraform-providers/terraform-provider-vsphere/vsphere/internal/helper/viapi"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/types"
)

const resourceVSphereVAppContainerName = "vsphere_vapp_container"

var vAppContainerCPUSharesLevelAllowedValues = []string{
	string(types.SharesLevelLow),
	string(types.SharesLevelNormal),
	string(types.SharesLevelHigh),
	string(types.SharesLevelCustom),
}

var vAppContainerMemorySharesLevelAllowedValues = []string{
	string(types.SharesLevelLow),
	string(types.SharesLevelNormal),
	string(types.SharesLevelHigh),
	string(types.SharesLevelCustom),
}

func resourceVSphereVAppContainer() *schema.Resource {
	s := map[string]*schema.Schema{
		"name": {
			Type:        schema.TypeString,
			Required:    true,
			Description: "Name of resource pool.",
		},
		"parent_resource_pool_id": {
			Type:        schema.TypeString,
			Description: "The ID of the parent resource pool of the compute resource the resource pool is in.",
			Required:    true,
		},
		"parent_folder": {
			Type:        schema.TypeString,
			Description: "The ID of the parent VM folder.",
			Optional:    true,
		},
		"cpu_share_level": {
			Type:         schema.TypeString,
			Description:  "The allocation level. The level is a simplified view of shares. Levels map to a pre-determined set of numeric values for shares. Can be one of low, normal, high, or custom.",
			Optional:     true,
			ValidateFunc: validation.StringInSlice(vAppContainerCPUSharesLevelAllowedValues, false),
			Default:      "normal",
		},
		"cpu_shares": {
			Type:        schema.TypeInt,
			Description: "The number of shares allocated. Used to determine resource allocation in case of resource contention. If this is set, cpu_share_level must be custom.",
			Computed:    true,
			Optional:    true,
		},
		"cpu_reservation": {
			Type:        schema.TypeInt,
			Description: "Amount of CPU (MHz) that is guaranteed available to the resource pool.",
			Optional:    true,
			Default:     0,
		},
		"cpu_expandable": {
			Type:        schema.TypeBool,
			Description: "Determines if the reservation on a resource pool can grow beyond the specified value, if the parent resource pool has unreserved resources.",
			Optional:    true,
			Default:     true,
		},
		"cpu_limit": {
			Type:        schema.TypeInt,
			Description: "The utilization of a resource pool will not exceed this limit, even if there are available resources. Set to -1 for unlimited.",
			Optional:    true,
			Default:     -1,
		},
		"memory_share_level": {
			Type:         schema.TypeString,
			Description:  "The allocation level. The level is a simplified view of shares. Levels map to a pre-determined set of numeric values for shares. Can be one of low, normal, high, or custom.",
			Optional:     true,
			ValidateFunc: validation.StringInSlice(vAppContainerMemorySharesLevelAllowedValues, false),
			Default:      "normal",
		},
		"memory_shares": {
			Type:        schema.TypeInt,
			Description: "The number of shares allocated. Used to determine resource allocation in case of resource contention. If this is set, memory_share_level must be custom.",
			Computed:    true,
			Optional:    true,
		},
		"memory_reservation": {
			Type:        schema.TypeInt,
			Description: "Amount of memory (MB) that is guaranteed available to the resource pool.",
			Optional:    true,
			Default:     0,
		},
		"memory_expandable": {
			Type:        schema.TypeBool,
			Description: "Determines if the reservation on a resource pool can grow beyond the specified value, if the parent resource pool has unreserved resources.",
			Optional:    true,
			Default:     true,
		},
		"memory_limit": {
			Type:        schema.TypeInt,
			Description: "The utilization of a resource pool will not exceed this limit, even if there are available resources. Set to -1 for unlimited.",
			Optional:    true,
			Default:     -1,
		},
		"resource_pool_id": {
			Type:        schema.TypeString,
			Description: "The managed resource ID of the resource pool created as part of the vApp Container.",
			Computed:    true,
		},
		vSphereTagAttributeKey:    tagsSchema(),
		customattribute.ConfigKey: customattribute.ConfigSchema(),
	}
	return &schema.Resource{
		Create: resourceVSphereVAppContainerCreate,
		Read:   resourceVSphereVAppContainerRead,
		Update: resourceVSphereVAppContainerUpdate,
		Delete: resourceVSphereVAppContainerDelete,
		Importer: &schema.ResourceImporter{
			State: resourceVSphereVAppContainerImport,
		},
		Schema: s,
	}
}

func resourceVSphereVAppContainerImport(d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
	client, err := resourceVSphereVAppContainerClient(meta)
	if err != nil {
		return nil, err
	}
	va, err := vappcontainer.FromPath(client, d.Id(), nil)
	if err != nil {
		return nil, err
	}
	d.SetId(va.Reference().Value)
	return []*schema.ResourceData{d}, nil
}

func resourceVSphereVAppContainerCreate(d *schema.ResourceData, meta interface{}) error {
	log.Printf("[DEBUG] %s: Beginning create", resourceVSphereVAppContainerIDString(d))
	client, err := resourceVSphereVAppContainerClient(meta)
	if err != nil {
		return err
	}
	prp, err := resourcepool.FromID(client, d.Get("parent_resource_pool_id").(string))
	if err != nil {
		return err
	}
	rpSpec := expandVAppContainerConfigSpec(d)
	vaSpec := &types.VAppConfigSpec{}
	var f *object.Folder
	if pf, ok := d.GetOk("parent_folder"); ok {
		f, err = folder.FromID(client, pf.(string))
		if err != nil {
			return err
		}
	} else {
		dc, err := getDatacenter(client, strings.Split(prp.InventoryPath, "/")[1])
		if err != nil {
			return err
		}
		f, err = folder.FromPath(client, "", folder.VSphereFolderTypeVM, dc)
		if err != nil {
			return err
		}
	}
	va, err := vappcontainer.Create(prp, d.Get("name").(string), rpSpec, vaSpec, f)
	if err != nil {
		return err
	}
	if err = resourceVSphereVAppContainerApplyTags(d, meta, va); err != nil {
		return err
	}
	d.SetId(va.Reference().Value)
	log.Printf("[DEBUG] %s: Create finished successfully", resourceVSphereVAppContainerIDString(d))
	return nil
}

func resourceVSphereVAppContainerRead(d *schema.ResourceData, meta interface{}) error {
	log.Printf("[DEBUG] %s: Beginning read", resourceVSphereVAppContainerIDString(d))
	client, err := resourceVSphereVAppContainerClient(meta)
	if err != nil {
		return err
	}
	va, err := vappcontainer.FromID(client, d.Id())
	if err != nil {
		if viapi.IsManagedObjectNotFoundError(err) {
			log.Printf("[DEBUG] %s: Resource has been deleted", resourceVSphereVAppContainerIDString(d))
			d.SetId("")
			return nil
		}
		return err
	}
	if err = resourceVSphereVAppContainerReadTags(d, meta, va); err != nil {
		return err
	}
	err = d.Set("name", va.Name())
	if err != nil {
		return err
	}
	vaProps, err := vappcontainer.Properties(va)
	if err != nil {
		return err
	}
	if err = d.Set("parent_resource_pool_id", vaProps.Parent.Value); err != nil {
		return err
	}
	err = d.Set("resource_pool_id", vaProps.ResourcePool.Value)
	if err != nil {
		return err
	}
	err = flattenVAppContainerConfigSpec(d, vaProps.Config)
	if err != nil {
		return err
	}
	log.Printf("[DEBUG] %s: Read finished successfully", resourceVSphereVAppContainerIDString(d))
	return nil
}

func resourceVSphereVAppContainerUpdate(d *schema.ResourceData, meta interface{}) error {
	log.Printf("[DEBUG] %s: Beginning update", resourceVSphereVAppContainerIDString(d))
	client, err := resourceVSphereVAppContainerClient(meta)
	if err != nil {
		return err
	}
	va, err := vappcontainer.FromID(client, d.Id())
	if err != nil {
		return err
	}
	if err = resourceVSphereVAppContainerApplyTags(d, meta, va); err != nil {
		return err
	}
	op, np := d.GetChange("parent_resource_pool_id")
	if op != np {
		log.Printf("[DEBUG] %s: Parent resource pool has changed. Moving from %s, to %s", resourceVSphereVAppContainerIDString(d), op, np)
		p, err := vappcontainer.FromID(client, np.(string))
		if err != nil {
			return err
		}
		err = resourcepool.MoveIntoResourcePool(p.ResourcePool, va.Reference())
		if err != nil {
			return err
		}
		log.Printf("[DEBUG] %s: Move finished successfully", resourceVSphereVAppContainerIDString(d))
	}

	vaSpec := types.VAppConfigSpec{}
	err = vappcontainer.Update(va, vaSpec)
	if err != nil {
		return err
	}
	log.Printf("[DEBUG] %s: Update finished successfully", resourceVSphereVAppContainerIDString(d))
	return nil
}

func resourceVSphereVAppContainerDelete(d *schema.ResourceData, meta interface{}) error {
	log.Printf("[DEBUG] %s: Beginning delete", resourceVSphereVAppContainerIDString(d))
	client, err := resourceVSphereVAppContainerClient(meta)
	if err != nil {
		return err
	}
	va, err := vappcontainer.FromID(client, d.Id())
	if err != nil {
		return err
	}
	err = resourceVSphereVAppContainerValidateEmpty(va)
	if err != nil {
		return err
	}
	err = vappcontainer.Delete(va)
	if err != nil {
		return err
	}
	log.Printf("[DEBUG] %s: Deleted successfully", resourceVSphereVAppContainerIDString(d))
	return nil
}

// resourceVSphereVAppContainerIDString prints a friendly string for the
// vsphere_virtual_machine resource.
func resourceVSphereVAppContainerIDString(d structure.ResourceIDStringer) string {
	return structure.ResourceIDString(d, resourceVSphereVAppContainerName)
}

func flattenVAppContainerConfigSpec(d *schema.ResourceData, obj types.ResourceConfigSpec) error {
	err := flattenVAppContainerMemoryAllocation(d, obj.MemoryAllocation)
	if err != nil {
		return err
	}
	return flattenVAppContainerCPUAllocation(d, obj.CpuAllocation)
}

func flattenVAppContainerCPUAllocation(d *schema.ResourceData, obj types.ResourceAllocationInfo) error {
	return structure.SetBatch(d, map[string]interface{}{
		"cpu_reservation": obj.Reservation,
		"cpu_expandable":  obj.ExpandableReservation,
		"cpu_limit":       obj.Limit,
		"cpu_shares":      obj.Shares.Shares,
		"cpu_share_level": obj.Shares.Level,
	})
}

func flattenVAppContainerMemoryAllocation(d *schema.ResourceData, obj types.ResourceAllocationInfo) error {
	return structure.SetBatch(d, map[string]interface{}{
		"memory_reservation": obj.Reservation,
		"memory_expandable":  obj.ExpandableReservation,
		"memory_limit":       obj.Limit,
		"memory_shares":      obj.Shares.Shares,
		"memory_share_level": obj.Shares.Level,
	})
}

func expandVAppContainerConfigSpec(d *schema.ResourceData) *types.ResourceConfigSpec {
	return expandResourcePoolConfigSpec(d)
}

func resourceVSphereVAppContainerClient(meta interface{}) (*govmomi.Client, error) {
	client := meta.(*VSphereClient).vimClient
	if err := viapi.ValidateVirtualCenter(client); err != nil {
		return nil, err
	}
	return client, nil
}

func resourceVSphereVAppContainerValidateEmpty(va *object.VirtualApp) error {
	ne, err := vappcontainer.HasChildren(va)
	if err != nil {
		return fmt.Errorf("error checking contents of resource pool: %s", err)
	}
	if ne {
		return fmt.Errorf("resource pool %q still has children resources. Please move or remove all items before deleting", va.InventoryPath)
	}
	return nil
}

// resourceVSphereVAppContainerApplyTags processes the tags step for both create
// and update for vsphere_resource_pool.
func resourceVSphereVAppContainerApplyTags(d *schema.ResourceData, meta interface{}, va *object.VirtualApp) error {
	tagsClient, err := tagsClientIfDefined(d, meta)
	if err != nil {
		return err
	}

	// Apply any pending tags now.
	if tagsClient == nil {
		log.Printf("[DEBUG] %s: Tags unsupported on this connection, skipping", resourceVSphereComputeClusterIDString(d))
		return nil
	}

	log.Printf("[DEBUG] %s: Applying any pending tags", resourceVSphereVAppContainerIDString(d))
	return processTagDiff(tagsClient, d, va)
}

// resourceVSphereVAppContainerReadTags reads the tags for
// vsphere_resource_pool.
func resourceVSphereVAppContainerReadTags(d *schema.ResourceData, meta interface{}, va *object.VirtualApp) error {
	if tagsClient, _ := meta.(*VSphereClient).TagsClient(); tagsClient != nil {
		log.Printf("[DEBUG] %s: Reading tags", resourceVSphereVAppContainerIDString(d))
		if err := readTagsForResource(tagsClient, va, d); err != nil {
			return err
		}
	} else {
		log.Printf("[DEBUG] %s: Tags unsupported on this connection, skipping tag read", resourceVSphereVAppContainerIDString(d))
	}
	return nil
}
