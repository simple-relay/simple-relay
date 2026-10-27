# Simple Relay: Reliable HTTP delivery

## hook
When a webhook arrives during a deploy, [#deploy] your application may not be ready. The sender still needs a quick, dependable response. [#response]

## setup
Run Simple Relay as one container. [#container] There is no broker to install [#no-broker] and no separate database to provision. Embedded SQLite is ready to store requests. [#sqlite]

## ack
Relay can verify [#verify] the signature, when configured. It saves the original payload [#persist] before sending a fast acknowledgement. The sender [#ack] does not wait for your application to recover.

## retry
Background workers [#worker] deliver the request. If your application is temporarily unavailable, [#retry] Relay retries with a bounded backoff. When it recovers, delivery continues. [#delivered]

## recovery
If all attempts fail, [#dlq] the request moves to the {DLQ|D L Q}, where failed deliveries stay visible. Inspect the failure. [#inspect] Replay it when you are ready. [#replay]

## close
Start with one command and be up in about two minutes. [#quickstart] Simple Relay makes HTTP delivery more reliable, without a broker to run. [#summary] Read the Quickstart to try it.
