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

  PROTOCOL_IP4 = 1; // IPv4 address, e.g., "1.1.1.1"
  PROTOCOL_IP6 = 2; // IPv6 address, e.g., "2606:4700:4700::1111"
  PROTOCOL_DNS4 = 3; // DNS address, e.g., "example.com"
  PROTOCOL_DNS6 = 4; // DNS address with IPv6, e.g., "example.com"

  PROTOCOL_TCP = 5; // TCP protocol and port, e.g., "7496"
  PROTOCOL_UDP = 6; // UDP protocol and port, e.g., "7496"
  PROTOCOL_QUIC = 7; // QUIC protocol and port, e.g., "7496" with optional associated Certificate Fingerprint

  PROTOCOL_HTTP = 8; // HTTP protocol, with streaming support
  PROTOCOL_GRPC = 9; // gRPC protocol, with streaming support
  PROTOCOL_WEBRTC = 10; // WebRTC Data Channel Transport
  PROTOCOL_WEBSOCKET = 11; // WebSocket protocol, with streaming support
  PROTOCOL_WEBTRANSPORT = 12; // WebTransport protocol, with streaming support

  PROTOCOL_BLE = 20; // Bluetooth Low Energy protocol WIP
  PROTOCOL_UWB = 21; // Ultra-Wideband protocol WIP
  PROTOCOL_LORA = 22; // LoRA protocol WIP
  PROTOCOL_ZWAVE = 23; // Z-Wave protocol WIP
  PROTOCOL_ZIGBEE = 24; // Zigbee protocol WIP

  PROTOCOL_SNRELAY = 30; // SuperNet Relay Protocol
}

message Address {
  Protocol protocol = 1; // Protocol type of the address
  bytes address = 2; // address data
  optional Address local = 3;
  optional bytes identifier = 4; // Unique identifier for the address
  optional bytes associated_data = 5; // Additional data associated with the address
}

message AddressList {
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
  Protocol: maddr.Protocol_PROTOCOL_TCP,
  Address: []byte("192.0.2.1:443"),
  Local: &maddr.Address{
    Protocol: maddr.Protocol_PROTOCOL_WEBRTC,
  },
}

addrList := &maddr.AddressList{
  Addresses: []*maddr.Address{addr},
  ClientProtocols: []maddr.Protocol{
    maddr.Protocol_PROTOCOL_TCP,
    maddr.Protocol_PROTOCOL_HTTP,
  },
}
```
