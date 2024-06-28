# failure mode recovery

# case 1
Controller not running/did not pick up the condition within the stale threshold

kubectl delete deployments.apps flasher

```buf.json
{
  "id": "44723968-a646-486d-887a-12e789988b71",
  "state": "pending",
  "conditions": [
    {
      "version": "1.1",
      "client": "",
      "id": "44723968-a646-486d-887a-12e789988b71",
      "target": "ede81024-f62a-4288-8730-3fab8cceab78",
      "kind": "firmwareInstall",
      "parameters": {
        "asset_id": "ede81024-f62a-4288-8730-3fab8cceab78",
        "reset_bmc_before_install": true,
        "firmware_set_id": "ce3a7a82-192b-427b-b965-6a58cb02c7de"
      },
      "state": "pending",
      "failOnCheckpointError": true,
      "updatedAt": "0001-01-01T00:00:00Z",
      "createdAt": "2024-05-08T04:30:09.718141106Z"
    },
    {
      "version": "1.1",
      "client": "",
      "id": "44723968-a646-486d-887a-12e789988b71",
      "target": "ede81024-f62a-4288-8730-3fab8cceab78",
      "kind": "inventory",
      "parameters": {
        "collect_bios_cfg": true,
        "collect_firmware_status": true,
        "inventory_method": "outofband",
        "asset_id": "ede81024-f62a-4288-8730-3fab8cceab78"
      },
      "state": "pending",
      "failOnCheckpointError": true,
      "updatedAt": "0001-01-01T00:00:00Z",
      "createdAt": "2024-05-08T05:58:09.718141106Z"
    }
  ]
}

```

swap timestamp to be older
```sh
nats --creds /root/nsc/nkeys/creds/KO/controllers/conditionorc.creds  kv get active-conditions ede81024-f62a-4288-8730-3fab8cceab78 --raw | sed -e 's/2024-05-08T09:06:47.702770532Z/2024-05-08T08:30:
47.702770532Z/' | nats --creds /root/nsc/nkeys/creds/KO/controllers/conditionorc.creds  kv put active-conditions ede81024-f62a-4288-8730-3fab8cceab78
```

.. wait for orchestrator reconcile to kick in, state should be failed
```sh
nats --creds /root/nsc/nkeys/creds/KO/controllers/conditionorc.creds  kv get active-conditions ede81024-f62a-4288-8730-3fab8cceab78 --raw | jq -Mr .state
failed
```
# case 2
Controller active, active-conditions record was purged

```buf.json
~ # nats --creds /root/nsc/nkeys/creds/KO/controllers/conditionorc.creds  kv get active-conditions ede81024-f62a-4288-8730-3fab8cceab78 --raw | jq .
{
  "id": "76da64b2-5178-40f4-ab25-73a3ae45e671",
  "state": "active",
  "conditions": [
    {
      "version": "1.1",
      "client": "",
      "id": "76da64b2-5178-40f4-ab25-73a3ae45e671",
      "target": "00000000-0000-0000-0000-000000000000",
      "kind": "inventory",
      "parameters": {
        "collect_bios_cfg": true,
        "collect_firmware_status": true,
        "inventory_method": "outofband",
        "asset_id": "ede81024-f62a-4288-8730-3fab8cceab78"
      },
      "state": "active",
      "status": {
        "msg": "Collecting inventory outofband for device"
      },
      "updatedAt": "2024-05-08T06:11:40.383769983Z",
      "createdAt": "2024-05-08T06:11:40.357440219Z"
    }
  ]
}
```


