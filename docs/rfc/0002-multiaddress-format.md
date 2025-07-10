# RFC-0002: MultiAddress Format Specification

## 1. Introduction
### 1.1. Design Principles
- Protocol-agnostic addressing
- Network ID isolation
- Public key based peer identification
- Extensible binary format

## 2. Protocol Buffers Schema
```protobuf
syntax = "proto3";

message MultiAddress {
  string network_id = 1;  // e.g., "i2p", "mainnet"
  repeated Component components = 2;
  bytes peer_id = 3;      // Base58-encoded public key hash

  message Component {
    oneof value {
      Protocol protocol = 1;
      string address = 2;
      uint32 port = 3;
      bytes binary_data = 4;
    }

    enum Protocol {
      IP4 = 0;
      IP6 = 1;
      TCP = 2;
      UDP = 3;
      WEBRTC = 4;
      HTTP = 5;
      HTTPS = 6;
      P2P = 7;
      I2P = 8;
    }
  }
}
```

## 3. Encoding/Decoding Rules
### 3.1. Binary Encoding
- Varint-prefixed Protobuf serialization
- Network byte order (big-endian)

### 3.2. Text Representation
```
/network/i2p/proto/tcp/192.0.2.1:443/proto/webrtc/udp/5000/p2p/QmPublicKey
```

## 4. Component Validation
| Protocol    | Required Fields     | Validation Rules              |
|-------------|---------------------|-------------------------------|
| IP4/IP6     | address             | Valid IPv4/IPv6 format        |
| TCP/UDP     | port                | 1-65535 port range            |
| WebRTC      | udp component       | Must precede UDP component    |
| P2P         | peer_id             | Base58 public key hash        |

## 5. Example Implementations
```go
// Go construction example
addr := &pb.MultiAddress{
  NetworkId: "i2p",
  Components: []*pb.MultiAddress_Component{
    {Value: &pb.MultiAddress_Component_Protocol{pb.MultiAddress_Component_TCP}},
    {Value: &pb.MultiAddress_Component_Port{443}},
    {Value: &pb.MultiAddress_Component_Protocol{pb.MultiAddress_Component_WEBRTC}},
  },
  PeerId: []byte("QmPublicKeyBase58"),
}
