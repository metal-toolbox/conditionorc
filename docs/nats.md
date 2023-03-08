
## Jetstream


We use the stream as a queue - queue group, with consumers as pull consumers.

all controllers subscribe to `com.hollow.sh.controllers.events.>` which forms a queue group

https://docs.nats.io/nats-concepts/core-nats/queue

#### Push vs Pull


https://natsbyexample.com/examples/jetstream/push-consumer/go

Where push consumers can get unwieldy and confusing is when the subscriber cannot keep up, message processing errors start occurring, or the active subscription gets interrupted. Messages start getting redelivered and being interleaving with new messages pushed from the stream.


In practice, ephemeral push consumers can be a lightweight and useful way to do one-off consumption of a subset of messages in a stream. However, if you have a durable use case, it is recommended to access pull consumers first which provides more control and implicit support for scaling out consumption.

https://natsbyexample.com/examples/jetstream/pull-consumer/go/

### stream config
 Retention policy:
  - LimitsPolicy or Interest policy?

```
~ # nats stream add -s nats://conditionorc:password@nats:4222
? Stream Name controllers
? Subjects com.hollow.sh.controllers.>
? Storage memory
? Replication 1
? Retention Policy Work Queue
? Discard Policy Old
? Stream Messages Limit -1
? Per Subject Messages Limit -1
? Total Stream Size -1
? Message TTL -1
? Max Message Size -1
? Duplicate tracking time window 2m0s
X Sorry, your reply was invalid: "?" is not a valid answer, please try again.
? Allow message Roll-ups No
? Allow message deletion Yes
? Allow purging subjects or the entire stream Yes
```


### publisher

Message deduplication
  - For deduplication all publishers should include the MsgId when publishing `PublishAsync(.., .., MsgId(""))`
  - The time window for duplicate tracking is 2m, set when creating the stream.

### Subscribers

 - All subscriptions are to be durable (not ephermal), that is they must subscribe with `Bind()` or by calling `Durable()`.
 - If the a subscription does not already exist when calling `Durable()`, its created and then removed once done.

 Q.
  - Can multiple subscribers use the same NATs consumer account?
  - Subscribers should be Async subscribers

To ensure that

On unsubscribing the JetStream consumer is automatically deleted, eonsumer is not created by the library, which means create the consumer with AddConsumer and bind to this consumer (using the nats.Bind() option).


## Stream parameters
```
            Retention: Limits
     Acknowledgements: true
       Discard Policy: Old
     Duplicate Window: 2m0s
    Allows Msg Delete: true
         Allows Purge: true
       Allows Rollups: false
```

## consumer parameters
```
        Durable Name: alloy
           Pull Mode: true
      Filter Subject: com.hollow.sh.events.>
      Deliver Policy: All
          Ack Policy: All
            Ack Wait: 30s
       Replay Policy: Instant
     Max Ack Pending: 1,000
   Max Waiting Pulls: 512

```

### consumer

Consumers are a view on the stream, one or more applications may bind to a single consumer

- The config option `deliver_subject` when adding a consumer makes the consumer push-based.
- A consumer has a filter on the stream, this is set by the config option `filter_subject`

`Subscribe*` methods by default will create ephermal consumers, they are methods to keep parity with
the methods provided for core NATS.

- Conditionorc will add a consumer with the group `controllers` which is then to be used by durable subscribers.


### Queue group

- Consumers can be created with a queue group
- S

### Messages

The conditionorc messages can be of two types, and subscribers are required to atleast subscribe to the commands subject

 - events
    Where events from conditionorc are published, these are informational.
 - commands
    Where commands from conditionorc for the controllers are published, this
    is actual work to be performed by the controller.

#### Commands

Commands are send with the subject `com.hollow.sh.conditionorc.commands.>`,


For example a firmware installer controller can subscribe to this subject and watch for commands
its responsible to take actions on, the suffix of the subject will indicate the condition name
that the conditionorc wants fullfilled.

The inventory out of band controller will listen for commands with the subject,
`com.hollow.sh.conditionorc.commands.inventoryOutofband`

The out of band firmware installer controller will listen for commands with the subjects,
`com.hollow.sh.conditionorc.commands.firmwareInstallOutofband`
`com.hollow.sh.conditionorc.commands.firmwareInstallInband`

```
{
  "status": "pending",
  "parameters": "{ firmwareInstallParameters }"
}
```

#### Events

Both the orchestrator and controllers may send events.

The orchestrator listens on `com.hollow.sh.controllers.events.>`.

The controllers publishes events with the subject suffixed by the condition they are
fullfiling, for example,

The inventory out of band controller will publish events which update the progress
of a inventory collection with the subject `com.hollow.sh.controllers.events.inventoryOutofband`.


### NOTES
 - Publish on a queue group expects one or more controllers to be present or the message won't be published.


### Troubleshooting

https://github.com/nats-io/nats.docs/blob/d8880e8006161940a92553b99360ad189eb65563/nats-concepts/jetstream/consumers.md

 ## filtered consumer not unique on workqueue stream
- In a workqueue stream filtering cannot overlap
  - the filters must not overlap between consumers.
  - the subject must be different between consumers.



 ## nats: multiple non-filtered consumers not allowed on workqueue stream:

# nats: consumer deliver subject forms a cycle: error adding consumer on nats jetstream
-

# nats: consumer filter subject is not a valid subset of the interest subjects



# nats: no stream matches subject: com.hollow.sh.controllers.replies.\u003e

make sure the stream is configured with all the required subjects

```
nats -s nats://serverservice:password@nats:4222 consumer info | grep Subjects

 Subjects: com.hollow.sh.controllers.commands.>, com.hollow.sh.controllers.replies.>
```

# nats: consumer must be deliver all on workqueue stream

A consumer must bind to a stream using the same Deliver policy as other consumers

# nats: invalid subscription type

If the stream is setup for push based and a subscriber attempts to subscribe as a pull based subscriber using


# nats: message was already acknowledged

This shows up in a push based configuration ... ?

# nats: must use pull subscribe to bind to pull based consumer: com.hollow.sh.controllers.replies.\u003e





## works - stream info

```
~ # nats -s nats://serverservice:password@nats:4222 stream info
? Select a Stream controllers
Information for Stream controllers created 2023-03-08 06:32:52

             Subjects: com.hollow.sh.controllers.commands.>, com.hollow.sh.controllers.replies.>
             Replicas: 1
              Storage: File

Options:

            Retention: WorkQueue
     Acknowledgements: true
       Discard Policy: Old
     Duplicate Window: 2m0s
    Allows Msg Delete: true
         Allows Purge: true
       Allows Rollups: false

Limits:

     Maximum Messages: unlimited
  Maximum Per Subject: unlimited
        Maximum Bytes: unlimited
          Maximum Age: unlimited
 Maximum Message Size: unlimited
    Maximum Consumers: unlimited


State:

             Messages: 0
                Bytes: 0 B
             FirstSeq: 5
              LastSeq: 4 @ 2023-03-08T07:11:57 UTC
     Active Consumers: 2


~ # nats -s nats://serverservice:password@nats:4222 stream info
? Select a Stream serverservice
Information for Stream serverservice created 2023-03-08 05:41:30

             Subjects: com.hollow.sh.serverservice.events.>
             Replicas: 1
              Storage: File

Options:

            Retention: Limits
     Acknowledgements: true
       Discard Policy: Old
     Duplicate Window: 2m0s
    Allows Msg Delete: true
         Allows Purge: true
       Allows Rollups: false

Limits:

     Maximum Messages: unlimited
  Maximum Per Subject: unlimited
        Maximum Bytes: unlimited
          Maximum Age: unlimited
 Maximum Message Size: unlimited
    Maximum Consumers: unlimited


State:

             Messages: 4
                Bytes: 2.0 KiB
             FirstSeq: 1 @ 2023-03-08T06:50:22 UTC
              LastSeq: 4 @ 2023-03-08T07:11:57 UTC
     Active Consumers: 1
   Number of Subjects: 1

```

## works - consumer info
```
~ # nats -s nats://serverservice:password@nats:4222 consumer info
? Select a Stream controllers
? Select a Consumer conditionorc
Information for Consumer controllers > conditionorc created 2023-03-08T06:32:52Z

Configuration:

                Name: conditionorc
    Delivery Subject: _INBOX.xE15IeXRuX7Yw0KtD6P7Mc
      Filter Subject: com.hollow.sh.controllers.replies.>
      Deliver Policy: All
          Ack Policy: Explicit
            Ack Wait: 30s
       Replay Policy: Instant
     Max Ack Pending: 1,000
        Flow Control: false

State:

   Last Delivered Message: Consumer sequence: 0 Stream sequence: 0
     Acknowledgment floor: Consumer sequence: 0 Stream sequence: 0
         Outstanding Acks: 0 out of maximum 1,000
     Redelivered Messages: 0
     Unprocessed Messages: 0
          Active Interest: Active

~ # nats -s nats://serverservice:password@nats:4222 consumer info

? Select a Stream controllers
? Select a Consumer controller-alloy
Information for Consumer controllers > controller-alloy created 2023-03-08T06:39:11Z

Configuration:

                Name: controller-alloy
    Delivery Subject: controllers.alloy
      Filter Subject: com.hollow.sh.controllers.commands.>
      Deliver Policy: All
 Deliver Queue Group: controllers
          Ack Policy: Explicit
            Ack Wait: 5m0s
       Replay Policy: Instant
     Max Ack Pending: 10
        Flow Control: false

State:

   Last Delivered Message: Consumer sequence: 4 Stream sequence: 4 Last delivery: 1h51m2s ago
     Acknowledgment floor: Consumer sequence: 4 Stream sequence: 4 Last Ack: 1h51m2s ago
         Outstanding Acks: 0 out of maximum 10
     Redelivered Messages: 0
     Unprocessed Messages: 0
          Active Interest: Active using Queue Group controllers



```