package vsphere

import (
	"log"
	"strings"

	"github.com/hashicorp/terraform/helper/schema"
	"github.com/terraform-providers/terraform-provider-vsphere/vsphere/internal/helper/customattribute"
	"github.com/terraform-providers/terraform-provider-vsphere/vsphere/internal/helper/resourcepool"
	"github.com/terraform-providers/terraform-provider-vsphere/vsphere/internal/helper/structure"
	"github.com/terraform-providers/terraform-provider-vsphere/vsphere/internal/helper/vappcontainer"
	"github.com/terraform-providers/terraform-provider-vsphere/vsphere/internal/helper/viapi"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/vim25/types"
)

const resourceVSphereVAppEntityName = "vsphere_vapp_entity"

func resourceVSphereVAppEntity() *schema.Resource {
	s := map[string]*schema.Schema{
		"target_id": {
			Type:        schema.TypeString,
			Description: "Managed object ID of the entity to power on or power off. This can be a virtual machine or a vApp.",
			Required:    true,
		},
		"container_id": {
			Type:        schema.TypeString,
			Description: "Managed object ID of the vApp container the entity is a member of.",
			Required:    true,
		},
		"start_action": {
			Type:        schema.TypeString,
			Description: "How to start the entity. Valid settings are none or powerOn. If set to none, then the entity does not participate in auto-start.",
			Default:     "powerOn",
			Optional:    true,
		},
		"start_delay": {
			Type:        schema.TypeInt,
			Description: "Delay in seconds before continuing with the next entity in the order of entities to be started.",
			Optional:    true,
		},
		"stop_action": {
			Type:        schema.TypeString,
			Description: "Defines the stop action for the entity. Can be set to none, powerOff, guestShutdown, or suspend. If set to none, then the entity does not participate in auto-stop.",
			Default:     "powerOff",
			Optional:    true,
		},
		"stop_delay": {
			Type:        schema.TypeInt,
			Description: "Delay in seconds before continuing with the next entity in the order of entities to be stopped.",
			Optional:    true,
		},
		"wait_for_guest": {
			Type:        schema.TypeBool,
			Description: "Determines if the VM should be marked as being started when VMware Tools are ready instead of waiting for start_delay. This property has no effect for vApps.",
			Optional:    true,
			Default:     false,
		},
		vSphereTagAttributeKey:    tagsSchema(),
		customattribute.ConfigKey: customattribute.ConfigSchema(),
	}
	return &schema.Resource{
		Create: resourceVSphereVAppEntityCreate,
		Read:   resourceVSphereVAppEntityRead,
		Update: resourceVSphereVAppEntityUpdate,
		Delete: resourceVSphereVAppEntityDelete,
		Importer: &schema.ResourceImporter{
			State: resourceVSphereVAppEntityImport,
		},
		Schema: s,
	}
}

func resourceVSphereVAppEntityImport(d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
	return nil, nil
}

func resourceVSphereVAppEntityCreate(d *schema.ResourceData, meta interface{}) error {
	log.Printf("[DEBUG] %s: Beginning create", resourceVSphereVAppEntityIDString(d))
	client, err := resourceVSphereVAppContainerClient(meta)
	if err != nil {
		return err
	}
	container, err := vappcontainer.FromID(client, d.Get("container_id").(string))
	if err != nil {
		return err
	}
	mo, err := vappcontainer.Properties(container)
	if err != nil {
		return err
	}
	entityConfig := types.VAppEntityConfigInfo{
		StartAction:     d.Get("start_action").(string),
		StartDelay:      int32(d.Get("start_delay").(int)),
		StopAction:      d.Get("start_action").(string),
		StopDelay:       int32(d.Get("start_delay").(int)),
		StartOrder:      int32(d.Get("start_order").(int)),
		WaitingForGuest: structure.GetBoolPtr(d, "wait_for_guest"),
	}
	mo.VAppConfig.EntityConfig = append(mo.VAppConfig.EntityConfig, entityConfig)
	updateSpec := types.VAppConfigSpec{
		EntityConfig: mo.VAppConfig.EntityConfig,
	}

	if err = vappcontainer.Update(container, updateSpec); err != nil {
		return err
	}

	log.Printf("[DEBUG] %s: Create finished successfully", resourceVSphereVAppContainerIDString(d))
	return nil
}

func resourceVSphereVAppEntityRead(d *schema.ResourceData, meta interface{}) error {
	log.Printf("[DEBUG] %s: Beginning read", resourceVSphereVAppEntityIDString(d))
	client, err := resourceVSphereVAppContainerClient(meta)
	if err != nil {
		return err
	}
	entity, err := resourceVSphereVAppEntityFind(client, d)
	if err != nil {
		return err
	}
	if entity == nil {
		log.Printf("[DEBUG] %s: Resource has been deleted", resourceVSphereVAppEntityIDString(d))
		d.SetId("")
		return nil
	}
	err = flattenVAppEntityConfigSpec(client, d, entity)
	if err != nil {
		return err
	}
	log.Printf("[DEBUG] %s: Read finished successfully", resourceVSphereVAppEntityIDString(d))
	return nil
}

func resourceVSphereVAppEntityUpdate(d *schema.ResourceData, meta interface{}) error {
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

func resourceVSphereVAppEntityDelete(d *schema.ResourceData, meta interface{}) error {
	return nil
}

func resourceVSphereVAppEntityFind(client *govmomi.Client, d *schema.ResourceData) (*types.VAppEntityConfigInfo, error) {
	parts := strings.SplitN(d.Id(), ":", 2)
	cid := parts[0]
	eid := parts[1]
	container, err := vappcontainer.FromID(client, cid)
	if err != nil {
		return nil, err
	}
	props, err := vappcontainer.Properties(container)
	if err != nil {
		return nil, err
	}
	entity := vappcontainer.EntityFromKey(eid, props)
	return entity, nil
}

// resourceVSphereVAppEntityIDString prints a friendly string for the
// vapp_entity resource.
func resourceVSphereVAppEntityIDString(d structure.ResourceIDStringer) string {
	return structure.ResourceIDString(d, resourceVSphereVAppEntityName)
}

func flattenVAppEntityConfigSpec(client *govmomi.Client, d *schema.ResourceData, obj *types.VAppEntityConfigInfo) error {
	target, err := vAppEntityChildReference(client, obj.Key.Value)
	if err != nil {
		return err
	}
	return structure.SetBatch(d, map[string]interface{}{
		"target_id":           target,
		"destroy_with_parent": obj.DestroyWithParent,
		"start_action":        obj.StartAction,
		"start_delay":         obj.StartDelay,
		"start_order":         obj.StartOrder,
		"stop_action":         obj.StopAction,
		"stop_delay":          obj.StopDelay,
		"wait_for_guest":      obj.WaitingForGuest,
	})
}

func expandVAppEntityConfigSpec(client *govmomi.Client, d *schema.ResourceData) (*types.VAppEntityConfigInfo, error) {
	target, err := vAppEntityChild(client, d.Get("target_id").(string))
	if err != nil {
		return nil, err
	}
	return &types.VAppEntityConfigInfo{
		Key:               target,
		DestroyWithParent: structure.GetBoolPtr(d, "destroy_with_parent"),
		StartAction:       d.Get("start_action").(string),
		StartDelay:        int32(d.Get("start_delay").(int)),
		StopAction:        d.Get("stop_action").(string),
		StopDelay:         int32(d.Get("stop_delay").(int)),
		WaitingForGuest:   structure.GetBoolPtr(d, "wait_for_guest"),
	}, nil
}

func resourceVSphereVAppEntityClient(meta interface{}) (*govmomi.Client, error) {
	client := meta.(*VSphereClient).vimClient
	if err := viapi.ValidateVirtualCenter(client); err != nil {
		return nil, err
	}
	return client, nil
}

func vAppEntityChildReference(client *govmomi.Client, ref string) (string, error) {
	return "", nil
}

func vAppEntityChild(client *govmomi.Client, entity string) (*types.ManagedObjectReference, error) {
	return &types.ManagedObjectReference{}, nil
}
