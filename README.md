### ConditionOrc

ConditionOrc provides the [conditions](https://github.com/metal-toolbox/architecture/blob/firmware-install-service/firmware-install-service.md#conditions) construct,
for consumers wanting to execute actions on server hardware.

It does this by,
 - Providing a CRUD API to setting conditions like `firmwareInstall`, `Inventory` on a server.
 - Follows up on with the controllers to nudge them to reconcile those conditions.

For more information on how this fits all together, see
[here](https://github.com/metal-toolbox/architecture/blob/firmware-install-service/firmware-install-service.md)

##### Purposes

- Expose a CRUD API for server conditions with the conditions stored in Serverservice as server Attributes.
- Keep track of controllers active per condition, redistributes work if a controller is unavailable.
- Lookup un-finalized conditions in Serverservice and publishes them with a time interval until they are finalized level based triggers.
- Garbage collect conditions in Serverservice that are found to be idle.
