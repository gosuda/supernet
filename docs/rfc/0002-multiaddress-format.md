# RFC-0002: Address Format Specification

## 1. Introduction
### 1.1. Design Principles
- Protocol-agnostic addressing
- Network ID isolation
- Relay support through address nesting
- Extensible binary format

## 2. Protocol Buffers Schema
```protobuf
syntax = "proto3";

package maddr;

enum Protocol {
  PROTOCOL_UNSPECIFIED = 0;
  PROTOCOL_IP4 = 1;
  PROTOCOL_IP6 = 2;
  PROTOCOL_TCP = 3;
  PROTOCOL_UDP = 4;
  PROTOCOL_WEBRTC_SIGNAL = 5;
  PROTOCOL_HTTP = 6;
  PROTOCOL_HTTPS = 7;
  PROTOCOL_WS = 8;
  PROTOCOL_WSS = 9;
}

message Address {
  uint64 network_id = 1;
  Protocol protocol = 2;
  optional Address relay_address = 3;
}

message AddressSet {
  repeated Address addresses = 1;
  repeated Protocol client_protocols = 2;
}
```

## 3. Encoding/Decoding Rules
### 3.1. Binary Encoding
- Varint-prefixed Protobuf serialization
- Network byte order (big-endian)

### 3.2. Text Representation
```
/network/1234/proto/tcp/192.0.2.1:443/relay/proto/webrtc-signal
```

## 4. Protocol Usage Guidelines
| Protocol            | Use Case                          | Requires Relay |
|---------------------|-----------------------------------|----------------|
| IP4/IP6             | Direct IPv4/IPv6 connections      | No             |
| TCP/UDP             | Standard TCP/UDP transport        | No             |
| WEBRTC_SIGNAL       | WebRTC signaling channel          | Yes            |
| HTTP/HTTPS          | HTTP-based transports             | Optional       |
| WS/WSS              | WebSocket connections             | Optional       |

## 5. Example Implementations
```go
// Go construction example
addr := &maddr.Address{
  NetworkId: 1234,
  Protocol: maddr.Protocol_PROTOCOL_TCP,
  RelayAddress: &maddr.Address{
    Protocol: maddr.Protocol_PROTOCOL_WEBRTC_SIGNAL,
  },
}

addrSet := &maddr.AddressSet{
  Addresses: []*maddr.Address{addr},
  ClientProtocols: []maddr.Protocol{
    maddr.Protocol_PROTOCOL_TCP,
    maddr.Protocol_PROTOCOL_HTTPS,
  },
}
```
