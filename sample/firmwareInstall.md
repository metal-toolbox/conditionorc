 #### Queue a firmware-set install

request
 ```bash
   curl -Lv https://conditionapi/api/v1/servers/a5e051cd-dcb1-4de2-b8fa-2571b7381653/condition/firmwareInstall
       -X POST
       -d '{
 		      "exclusive": true,
 		      "parameters": {
 			     "asset_id": "a5e051cd-dcb1-4de2-b8fa-2571b7381653",
 			    "firmware_set_id": "7b4cca35-eb46-49fc-8c30-a4e5ec689303"
 		      }
 	       }'
```

response
```json
 {
 	"message": "condition set",
 	"records": {
 		"serverID": "ede81024-f62a-4288-8730-3fab8cceab78",
 		"conditions": [
 			{
 				"version": "1",
 				"id": "b9e5ac5a-e8a3-4506-a83e-576562fa5701",
 				"kind": "firmwareInstall",
 				"parameters": {
 					"asset_id": "ede81024-f62a-4288-8730-3fab8cceab78",
 					"firmware_set_id": "7b4cca35-eb46-49fc-8c30-a4e5ec689303"
 				},
 				"state": "pending",
 				"exclusive": true,
 				"resourceVersion": 1695734722314951200,
 				"updatedAt": "0001-01-01T00:00:00Z",
 				"createdAt": "0001-01-01T00:00:00Z"
 			}
 		]
 	}
 }
```

### Query firmware-set install status

```bash
curl -Lv https://conditionapi/api/v1/servers/ede81024-f62a-4288-8730-3fab8cceab78/condition/firmwareInstall
{
  "records": {
    "serverID": "ede81024-f62a-4288-8730-3fab8cceab78",
    "conditions": [
      {
        "version": "1",
        "id": "c3e3e7cc-f3be-4276-9746-ece745f485bc",
        "kind": "firmwareInstall",
        "parameters": {
          "asset_id": "ede81024-f62a-4288-8730-3fab8cceab78",
          "firmware_set_id": "7b4cca35-eb46-49fc-8c30-a4e5ec689303",
        },
        "state": "succeeded",
        "status": {
          "msg": "component: bios, completed firmware install, version: 2.12.4"
        },
        "exclusive": true,
        "resourceVersion": 1695813453549651700,
        "updatedAt": "2023-09-27T10:46:40.878708Z",
        "createdAt": "2023-09-27T10:46:40.878708Z"
      }
    ]
  }
}
```