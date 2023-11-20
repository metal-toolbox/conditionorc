 #### Queue a firmware-set install

request
```bash
   curl -i --json @fwInstall.json https://<conditionAPI URL>/api/v1/servers/<server_UUID>/firmwareInstall
```

fwInstall.json
```json
   {
       "asset_id": "<server UUID>",
       "firmware_set_id": "<firmware set UUID>",
       "reset_bmc_before_install": true,
       "dry_run": true
   }
```

response
```json
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8
Date: Mon, 20 Nov 2023 19:18:01 GMT
Content-Length: 40

{"message":"firmware install scheduled"}
```

### Query firmware-set install status

```bash
./mctl --config config.yaml install status -s <server UUID>
{
  "id": "<internal condition UUID>",
  "kind": "firmwareInstall",
  "state": "succeeded",
  "parameters": {
    "asset_id": "<server UUID>",
    "reset_bmc_before_install": true,
    "dry_run": true,
    "firmware_set_id": "<firmware set UUID>"
  },
  "status": {
    "msg": "component: bios, completed firmware install, version: 2.8.5"
  },
  "updated_at": "2023-11-20T19:41:26.641921011Z",
  "created_at": "2023-11-20T19:39:36.206522668Z"
}
```