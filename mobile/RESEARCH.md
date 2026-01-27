# Gas Town Mobile Backend: RPC Framework Research

## Executive Summary

**Recommendation: Connect-RPC** for the Gas Town mobile backend.

Connect-RPC provides the best balance of features for our use case: gRPC wire compatibility, HTTP/1.1 fallback for simpler infrastructure, JSON debugging support, smaller mobile footprint, and good client libraries for iOS/Android.

## Framework Comparison

| Feature | gRPC | Connect-RPC | Twirp |
|---------|------|-------------|-------|
| **Maturity** | Very mature (v1.78) | Stable (v1.x, CNCF sandbox) | Stable but slow dev (v8.1.3, Oct 2022) |
| **HTTP/1.1** | No | Yes | Yes |
| **HTTP/2** | Required | Yes | Yes |
| **HTTP/3** | No | Yes | No |
| **Streaming** | Full bidirectional | Full bidirectional | None (unary only) |
| **JSON support** | Limited | Native | Yes |
| **Browser support** | Requires proxy | Direct | Manual HTTP |
| **Mobile binary size** | +1-5MB | Smaller | Smallest |
| **iOS client** | grpc-ios (C++) | connect-swift (stable) | Manual HTTP |
| **Android client** | grpc-android | connect-kotlin (beta) | twirp-kmp |
| **Debugging** | Difficult (binary) | Easy (curl/JSON) | Easy (curl/JSON) |

## Detailed Analysis

### gRPC

**Pros:**
- Industry standard, battle-tested at scale
- Excellent Go support (grpc-go v1.78)
- Strong typing via protobuf
- Bi-directional streaming
- Multi-language codegen

**Cons:**
- Requires HTTP/2 (firewall issues in some corporate environments)
- Adds 1-5MB to mobile app binary
- No browser support without gRPC-Web + Envoy proxy
- Binary protocol makes debugging harder
- Cannot use curl or browser DevTools

### Connect-RPC

**Pros:**
- gRPC wire-compatible (same server handles gRPC clients)
- Works over HTTP/1.1, HTTP/2, and HTTP/3
- Native JSON support for debugging with curl
- Built on Go's standard `net/http` (works with existing middleware)
- Smaller mobile client footprint
- Direct browser support (no proxy needed)
- Single package, minimal configuration
- connect-swift is stable; connect-kotlin in beta

**Cons:**
- Kotlin client still in beta (may have breaking changes)
- Smaller ecosystem than gRPC
- Cannot customize RPC paths (gRPC compatibility constraint)
- Newer, less production mileage at massive scale

### Twirp

**Pros:**
- Extremely simple setup
- HTTP/1.1 compatible
- JSON debugging support
- Minimal dependencies, fast builds
- Battle-tested at Twitch

**Cons:**
- **No streaming** - disqualifying for our real-time update needs
- Development has slowed significantly (last release Oct 2022)
- Smaller ecosystem
- Limited mobile client support

## Recommendation: Connect-RPC

Connect-RPC is the optimal choice for Gas Town mobile because:

1. **Real-time updates require streaming** - Twirp is eliminated immediately
2. **Debugging** - JSON support means we can test with curl during development
3. **Infrastructure simplicity** - HTTP/1.1 fallback works through any proxy/LB
4. **Mobile footprint** - Smaller than native gRPC
5. **Future flexibility** - gRPC wire compatibility means we can switch if needed
6. **Browser potential** - If we ever want a web dashboard, Connect works directly

### Migration Path

If Connect-RPC proves insufficient at scale, migration to pure gRPC is straightforward since:
- Same .proto definitions
- Same server handles both protocols
- Only client code needs updating

## API Surface Design

Based on analysis of gt CLI commands, the mobile API should expose:

### Priority 1: Core Operations

| Service | Operations | Use Case |
|---------|-----------|----------|
| **StatusService** | GetTownStatus, GetRigStatus, GetAgentStatus | Dashboard overview |
| **MailService** | ListInbox, ReadMessage, SendMessage, MarkRead | Notifications |
| **DecisionService** | ListPending, GetDecision, Resolve | Approve from phone |

### Priority 2: Work Tracking

| Service | Operations | Use Case |
|---------|-----------|----------|
| **ConvoyService** | ListConvoys, GetConvoyStatus | Track work batches |
| **BeadsService** | GetIssue, ListIssues | View issue details |

### Priority 3: Management

| Service | Operations | Use Case |
|---------|-----------|----------|
| **CrewService** | ListCrew, StartSession, StopSession | Manage crew workers |
| **RigService** | ListRigs, GetRig, BootRig | Rig management |

### Streaming Endpoints

For real-time updates, these should use server streaming:

- `StatusService.WatchStatus` - Stream status changes
- `MailService.WatchInbox` - Stream new mail notifications
- `DecisionService.WatchDecisions` - Stream new decision requests

## Authentication

Recommended approach: **API Keys with mTLS option**

1. **Development**: Simple API key in header (`X-GT-API-Key`)
2. **Production**: mTLS for server-to-mobile authentication
3. **OAuth option**: If integrating with external identity (GitHub, Google)

API key generation:
```bash
gt mobile keygen --name "Steve's iPhone" --expires 90d
```

Keys stored in town-level config, revocable via `gt mobile revoke`.

## Architecture

Recommended: **Sidecar daemon on town host**

```
┌─────────────────────────────────────────────┐
│ Town Host                                    │
│                                              │
│  ┌──────────┐     ┌──────────────────────┐  │
│  │ gt CLI   │────▶│ Existing gt commands │  │
│  └──────────┘     └──────────────────────┘  │
│                              ▲               │
│  ┌──────────────────────────┐│               │
│  │ gt-mobile-server (daemon)││               │
│  │  - Connect-RPC server    ││               │
│  │  - Port 8443 (TLS)       │┘               │
│  │  - Reuses gt internal    │                │
│  │    packages directly     │                │
│  └──────────────────────────┘                │
│              ▲                               │
└──────────────│───────────────────────────────┘
               │ HTTPS/Connect
               │
        ┌──────┴──────┐
        │ Mobile App  │
        │ (iOS/Android)│
        └─────────────┘
```

**Why sidecar:**
- Direct access to town filesystem and beads
- Reuses existing Go packages (no duplication)
- Runs under same user permissions
- Simple deployment: `gt mobile start`

## Security Considerations

1. **Authentication**: API keys with expiration, stored securely on device
2. **Authorization**: Configurable per-operation permissions (read-only vs full)
3. **Rate limiting**: Per-key rate limits to prevent abuse
4. **Audit logging**: All mobile operations logged
5. **TLS**: Required for production (self-signed OK with pinning)
6. **Decision restrictions**: Optionally limit which decisions mobile can resolve

## Next Steps

1. Draft .proto files for core services
2. Build proof-of-concept server exposing `gt status`
3. Test with curl and grpcurl
4. Evaluate connect-swift/connect-kotlin client generation
