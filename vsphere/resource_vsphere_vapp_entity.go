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
		"destroy_with_parent": {
			Type:        schema.TypeBool,
			Description: "Whether the entity should be removed, when this vApp is removed. This is only set for linked children.",
			Default:     false,
			Optional:    true,
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

func resourceVSphereVAppEntityCreate(d *schema.ResourceData, meta interface{}) error {
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
		//pf = fmt.Sprintf("/%s/vm", dc.Name())
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

func resourceVSphereVAppEntityRead(d *schema.ResourceData, meta interface{}) error {
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
	err = flattenVAppContainerConfigSpec(d, vaProps.Config)
	if err != nil {
		return err
	}
	log.Printf("[DEBUG] %s: Read finished successfully", resourceVSphereVAppContainerIDString(d))
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

// resourceVSphereVAppEntityIDString prints a friendly string for the
// vapp_entity resource.
func resourceVSphereVAppEntityIDString(d structure.ResourceIDStringer) string {
	return structure.ResourceIDString(d, resourceVSphereVAppEntityName)
}

func flattenVAppEntityConfigSpec(client *govmomi.Client, d *schema.ResourceData, obj types.VAppEntityConfigInfo) error {
	target, err := vAppEntityChildReference(client, obj.Key)
	if err != nil {
		return err
	}
	return structure.SetBatch(d, map[string]interface{}{
		"target_id":           target,
		"destroy_with_parent": obj.DestroyWithParent,
		"startAction":         obj.StartAction,
		"startDelay":          obj.StartDelay,
		"stopAction":          obj.StopAction,
		"stopDelay":           obj.StopDelay,
		"wait_for_guest":      obj.WaitingForGuest,
	})
}

func expandVAppEntityConfigSpec(client, d *schema.ResourceData) *types.VAppEntityConfigInfo {
	target, err := vAppEntityChild(client, d.Get("target_id").(string))
	if err != nil {
		return err
	}
	return &types.VAppEntityConfigInfo{
		Key:               target,
		DestroyWithParent: structure.GetBoolPtr(d, "destroy_with_parent"),
		StartAction:       d.Get("start_action").(string),
		StartDelay:        int32(d.Get("start_delay").(int)),
		StopAction:        d.Get("stop_action").(string),
		StopDelay:         int32(d.Get("stop_delay").(int)),
		WaitingForGuest:   structure.GetBoolPtr(d, "wait_for_guest"),
	}
}

func resourceVSphereVAppEntityClient(meta interface{}) (*govmomi.Client, error) {
	client := meta.(*VSphereClient).vimClient
	if err := viapi.ValidateVirtualCenter(client); err != nil {
		return nil, err
	}
	return client, nil
}

func vAppEntityChildReference(client *govmomi.Client, d *schema.ResourceData) (string, error) {
}

func vAppEntityChild(client *govmomi.Client, entity string) (string, error) {
}
