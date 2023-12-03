 #### Queue a server-enroll

request
```bash
   curl -i -X POST --header 'Content-Type: application/json' \
'localhost:9001/api/v1/serverEnroll/<server UUID>'  \
    -d '{"parameters":{"facility":"sandbox","bmc-ip":"fakeip","bmc-user":"root","bmc-pwd":"fakepwd"}}'
```

response
```json
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8
Date: Wed, 29 Nov 2023 04:36:40 GMT
Content-Length: 454

{"message":"condition set","records":{"serverID":"b3744970-47a0-4261-abd5-de19d7f7c6be","conditions":[{"version":"1","client":"","id":"fakeid","kind":"inventory","parameters":{"collect_bios_cfg":true,"collect_firmware_status":true,"inventory_method":"outofband","asset_id":"fakeassetid"},"state":"pending","resourceVersion":0,"updatedAt":"0001-01-01T00:00:00Z","createdAt":"0001-01-01T00:00:00Z"}]}}
```

### Query delete-server

```bash
curl -i -X DELETE 'localhost:9001/api/v1/servers/<server UUID>'
```

response
```json
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8
Date: Mon, 28 Nov 2023 18:25:08 GMT
Content-Length: 89

{"message":"server detele","records":{"serverID":"b3744970-47a0-4261-abd5-de19d7f7c6bd"}}
```