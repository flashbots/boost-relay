# Boost Relay

[![Goreport status](https://goreportcard.com/badge/github.com/flashbots/boost-relay)](https://goreportcard.com/report/github.com/flashbots/boost-relay)
[![Test status](https://github.com/flashbots/boost-relay/workflows/Checks/badge.svg)](https://github.com/flashbots/boost-relay/actions?query=workflow%3A%22Checks%22)

Flashbots internal PBS/[mev-boost](https://github.com/flashbots/mev-boost/) relay.

* Exposes a Builder REST API for mev-boost (proposers)
* Exposes an API for builders to send blocks

More information:

* https://www.notion.so/flashbots/Relay-API-Brainstorms-cf5edd57360140668c6d6b78fd04f312
* https://www.notion.so/flashbots/Relay-Design-Docs-623487c51b92423fabeb8da9c54af7f4

Contains three services:

* `registerValidator` receiver: needs to handle large amounts of requests, because all validators might want to register at the same time
* proposer API: `getHeader` and `getPayload`
* block builder API: `getValidatorsForEpoch` and `submitNewBlock`

## Getting started

This uses redis to store validator registrations. First run redis with docker:

```bash
docker run -d -p 6379:6379 redis:7
```

To run on kiln testnet:

```bash
go run cmd/relay/main.go -kiln
curl localhost:9062/eth/v1/builder/status
```
