// Code generated by protoc-gen-go. DO NOT EDIT.
// source: metaservice.proto

package lnrpc

import (
	context "context"
	fmt "fmt"
	proto "github.com/golang/protobuf/proto"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
	math "math"
)

// Reference imports to suppress errors if they are not otherwise used.
var _ = proto.Marshal
var _ = fmt.Errorf
var _ = math.Inf

// This is a compile-time assertion to ensure that this generated file
// is compatible with the proto package it is being compiled against.
// A compilation error at this line likely means your copy of the
// proto package needs to be updated.
const _ = proto.ProtoPackageIsVersion3 // please upgrade the proto package

type GetInfo2Request struct {
	InfoResponse         *GetInfoResponse `protobuf:"bytes,1,opt,name=InfoResponse,proto3" json:"InfoResponse,omitempty"`
	XXX_NoUnkeyedLiteral struct{}         `json:"-"`
	XXX_unrecognized     []byte           `json:"-"`
	XXX_sizecache        int32            `json:"-"`
}

func (m *GetInfo2Request) Reset()         { *m = GetInfo2Request{} }
func (m *GetInfo2Request) String() string { return proto.CompactTextString(m) }
func (*GetInfo2Request) ProtoMessage()    {}
func (*GetInfo2Request) Descriptor() ([]byte, []int) {
	return fileDescriptor_b3fb5294949b9545, []int{0}
}

func (m *GetInfo2Request) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_GetInfo2Request.Unmarshal(m, b)
}
func (m *GetInfo2Request) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_GetInfo2Request.Marshal(b, m, deterministic)
}
func (m *GetInfo2Request) XXX_Merge(src proto.Message) {
	xxx_messageInfo_GetInfo2Request.Merge(m, src)
}
func (m *GetInfo2Request) XXX_Size() int {
	return xxx_messageInfo_GetInfo2Request.Size(m)
}
func (m *GetInfo2Request) XXX_DiscardUnknown() {
	xxx_messageInfo_GetInfo2Request.DiscardUnknown(m)
}

var xxx_messageInfo_GetInfo2Request proto.InternalMessageInfo

func (m *GetInfo2Request) GetInfoResponse() *GetInfoResponse {
	if m != nil {
		return m.InfoResponse
	}
	return nil
}

type NeutrinoBan struct {
	Addr                 string   `protobuf:"bytes,1,opt,name=addr,proto3" json:"addr,omitempty"`
	Reason               string   `protobuf:"bytes,2,opt,name=reason,proto3" json:"reason,omitempty"`
	EndTime              string   `protobuf:"bytes,3,opt,name=end_time,json=endTime,proto3" json:"end_time,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *NeutrinoBan) Reset()         { *m = NeutrinoBan{} }
func (m *NeutrinoBan) String() string { return proto.CompactTextString(m) }
func (*NeutrinoBan) ProtoMessage()    {}
func (*NeutrinoBan) Descriptor() ([]byte, []int) {
	return fileDescriptor_b3fb5294949b9545, []int{1}
}

func (m *NeutrinoBan) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_NeutrinoBan.Unmarshal(m, b)
}
func (m *NeutrinoBan) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_NeutrinoBan.Marshal(b, m, deterministic)
}
func (m *NeutrinoBan) XXX_Merge(src proto.Message) {
	xxx_messageInfo_NeutrinoBan.Merge(m, src)
}
func (m *NeutrinoBan) XXX_Size() int {
	return xxx_messageInfo_NeutrinoBan.Size(m)
}
func (m *NeutrinoBan) XXX_DiscardUnknown() {
	xxx_messageInfo_NeutrinoBan.DiscardUnknown(m)
}

var xxx_messageInfo_NeutrinoBan proto.InternalMessageInfo

func (m *NeutrinoBan) GetAddr() string {
	if m != nil {
		return m.Addr
	}
	return ""
}

func (m *NeutrinoBan) GetReason() string {
	if m != nil {
		return m.Reason
	}
	return ""
}

func (m *NeutrinoBan) GetEndTime() string {
	if m != nil {
		return m.EndTime
	}
	return ""
}

type NeutrinoQuery struct {
	Peer                 string   `protobuf:"bytes,1,opt,name=peer,proto3" json:"peer,omitempty"`
	Command              string   `protobuf:"bytes,2,opt,name=command,proto3" json:"command,omitempty"`
	ReqNum               uint32   `protobuf:"varint,3,opt,name=req_num,json=reqNum,proto3" json:"req_num,omitempty"`
	CreateTime           uint32   `protobuf:"varint,4,opt,name=create_time,json=createTime,proto3" json:"create_time,omitempty"`
	LastRequestTime      uint32   `protobuf:"varint,5,opt,name=last_request_time,json=lastRequestTime,proto3" json:"last_request_time,omitempty"`
	LastResponseTime     uint32   `protobuf:"varint,6,opt,name=last_response_time,json=lastResponseTime,proto3" json:"last_response_time,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *NeutrinoQuery) Reset()         { *m = NeutrinoQuery{} }
func (m *NeutrinoQuery) String() string { return proto.CompactTextString(m) }
func (*NeutrinoQuery) ProtoMessage()    {}
func (*NeutrinoQuery) Descriptor() ([]byte, []int) {
	return fileDescriptor_b3fb5294949b9545, []int{2}
}

func (m *NeutrinoQuery) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_NeutrinoQuery.Unmarshal(m, b)
}
func (m *NeutrinoQuery) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_NeutrinoQuery.Marshal(b, m, deterministic)
}
func (m *NeutrinoQuery) XXX_Merge(src proto.Message) {
	xxx_messageInfo_NeutrinoQuery.Merge(m, src)
}
func (m *NeutrinoQuery) XXX_Size() int {
	return xxx_messageInfo_NeutrinoQuery.Size(m)
}
func (m *NeutrinoQuery) XXX_DiscardUnknown() {
	xxx_messageInfo_NeutrinoQuery.DiscardUnknown(m)
}

var xxx_messageInfo_NeutrinoQuery proto.InternalMessageInfo

func (m *NeutrinoQuery) GetPeer() string {
	if m != nil {
		return m.Peer
	}
	return ""
}

func (m *NeutrinoQuery) GetCommand() string {
	if m != nil {
		return m.Command
	}
	return ""
}

func (m *NeutrinoQuery) GetReqNum() uint32 {
	if m != nil {
		return m.ReqNum
	}
	return 0
}

func (m *NeutrinoQuery) GetCreateTime() uint32 {
	if m != nil {
		return m.CreateTime
	}
	return 0
}

func (m *NeutrinoQuery) GetLastRequestTime() uint32 {
	if m != nil {
		return m.LastRequestTime
	}
	return 0
}

func (m *NeutrinoQuery) GetLastResponseTime() uint32 {
	if m != nil {
		return m.LastResponseTime
	}
	return 0
}

type NeutrinoInfo struct {
	Peers                []*PeerDesc      `protobuf:"bytes,1,rep,name=peers,proto3" json:"peers,omitempty"`
	Bans                 []*NeutrinoBan   `protobuf:"bytes,2,rep,name=bans,proto3" json:"bans,omitempty"`
	Queries              []*NeutrinoQuery `protobuf:"bytes,3,rep,name=queries,proto3" json:"queries,omitempty"`
	BlockHash            string           `protobuf:"bytes,4,opt,name=block_hash,json=blockHash,proto3" json:"block_hash,omitempty"`
	Height               int32            `protobuf:"varint,5,opt,name=height,proto3" json:"height,omitempty"`
	BlockTimestamp       string           `protobuf:"bytes,6,opt,name=block_timestamp,json=blockTimestamp,proto3" json:"block_timestamp,omitempty"`
	IsSyncing            bool             `protobuf:"varint,7,opt,name=is_syncing,json=isSyncing,proto3" json:"is_syncing,omitempty"`
	XXX_NoUnkeyedLiteral struct{}         `json:"-"`
	XXX_unrecognized     []byte           `json:"-"`
	XXX_sizecache        int32            `json:"-"`
}

func (m *NeutrinoInfo) Reset()         { *m = NeutrinoInfo{} }
func (m *NeutrinoInfo) String() string { return proto.CompactTextString(m) }
func (*NeutrinoInfo) ProtoMessage()    {}
func (*NeutrinoInfo) Descriptor() ([]byte, []int) {
	return fileDescriptor_b3fb5294949b9545, []int{3}
}

func (m *NeutrinoInfo) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_NeutrinoInfo.Unmarshal(m, b)
}
func (m *NeutrinoInfo) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_NeutrinoInfo.Marshal(b, m, deterministic)
}
func (m *NeutrinoInfo) XXX_Merge(src proto.Message) {
	xxx_messageInfo_NeutrinoInfo.Merge(m, src)
}
func (m *NeutrinoInfo) XXX_Size() int {
	return xxx_messageInfo_NeutrinoInfo.Size(m)
}
func (m *NeutrinoInfo) XXX_DiscardUnknown() {
	xxx_messageInfo_NeutrinoInfo.DiscardUnknown(m)
}

var xxx_messageInfo_NeutrinoInfo proto.InternalMessageInfo

func (m *NeutrinoInfo) GetPeers() []*PeerDesc {
	if m != nil {
		return m.Peers
	}
	return nil
}

func (m *NeutrinoInfo) GetBans() []*NeutrinoBan {
	if m != nil {
		return m.Bans
	}
	return nil
}

func (m *NeutrinoInfo) GetQueries() []*NeutrinoQuery {
	if m != nil {
		return m.Queries
	}
	return nil
}

func (m *NeutrinoInfo) GetBlockHash() string {
	if m != nil {
		return m.BlockHash
	}
	return ""
}

func (m *NeutrinoInfo) GetHeight() int32 {
	if m != nil {
		return m.Height
	}
	return 0
}

func (m *NeutrinoInfo) GetBlockTimestamp() string {
	if m != nil {
		return m.BlockTimestamp
	}
	return ""
}

func (m *NeutrinoInfo) GetIsSyncing() bool {
	if m != nil {
		return m.IsSyncing
	}
	return false
}

type WalletInfo struct {
	CurrentBlockHash      string       `protobuf:"bytes,1,opt,name=current_block_hash,json=currentBlockHash,proto3" json:"current_block_hash,omitempty"`
	CurrentHeight         int32        `protobuf:"varint,2,opt,name=current_height,json=currentHeight,proto3" json:"current_height,omitempty"`
	CurrentBlockTimestamp string       `protobuf:"bytes,3,opt,name=current_block_timestamp,json=currentBlockTimestamp,proto3" json:"current_block_timestamp,omitempty"`
	WalletVersion         int32        `protobuf:"varint,4,opt,name=wallet_version,json=walletVersion,proto3" json:"wallet_version,omitempty"`
	WalletStats           *WalletStats `protobuf:"bytes,5,opt,name=wallet_stats,json=walletStats,proto3" json:"wallet_stats,omitempty"`
	XXX_NoUnkeyedLiteral  struct{}     `json:"-"`
	XXX_unrecognized      []byte       `json:"-"`
	XXX_sizecache         int32        `json:"-"`
}

func (m *WalletInfo) Reset()         { *m = WalletInfo{} }
func (m *WalletInfo) String() string { return proto.CompactTextString(m) }
func (*WalletInfo) ProtoMessage()    {}
func (*WalletInfo) Descriptor() ([]byte, []int) {
	return fileDescriptor_b3fb5294949b9545, []int{4}
}

func (m *WalletInfo) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_WalletInfo.Unmarshal(m, b)
}
func (m *WalletInfo) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_WalletInfo.Marshal(b, m, deterministic)
}
func (m *WalletInfo) XXX_Merge(src proto.Message) {
	xxx_messageInfo_WalletInfo.Merge(m, src)
}
func (m *WalletInfo) XXX_Size() int {
	return xxx_messageInfo_WalletInfo.Size(m)
}
func (m *WalletInfo) XXX_DiscardUnknown() {
	xxx_messageInfo_WalletInfo.DiscardUnknown(m)
}

var xxx_messageInfo_WalletInfo proto.InternalMessageInfo

func (m *WalletInfo) GetCurrentBlockHash() string {
	if m != nil {
		return m.CurrentBlockHash
	}
	return ""
}

func (m *WalletInfo) GetCurrentHeight() int32 {
	if m != nil {
		return m.CurrentHeight
	}
	return 0
}

func (m *WalletInfo) GetCurrentBlockTimestamp() string {
	if m != nil {
		return m.CurrentBlockTimestamp
	}
	return ""
}

func (m *WalletInfo) GetWalletVersion() int32 {
	if m != nil {
		return m.WalletVersion
	}
	return 0
}

func (m *WalletInfo) GetWalletStats() *WalletStats {
	if m != nil {
		return m.WalletStats
	}
	return nil
}

type GetInfo2Responce struct {
	Neutrino             *NeutrinoInfo    `protobuf:"bytes,1,opt,name=neutrino,proto3" json:"neutrino,omitempty"`
	Wallet               *WalletInfo      `protobuf:"bytes,2,opt,name=wallet,proto3" json:"wallet,omitempty"`
	Lightning            *GetInfoResponse `protobuf:"bytes,3,opt,name=lightning,proto3" json:"lightning,omitempty"`
	XXX_NoUnkeyedLiteral struct{}         `json:"-"`
	XXX_unrecognized     []byte           `json:"-"`
	XXX_sizecache        int32            `json:"-"`
}

func (m *GetInfo2Responce) Reset()         { *m = GetInfo2Responce{} }
func (m *GetInfo2Responce) String() string { return proto.CompactTextString(m) }
func (*GetInfo2Responce) ProtoMessage()    {}
func (*GetInfo2Responce) Descriptor() ([]byte, []int) {
	return fileDescriptor_b3fb5294949b9545, []int{5}
}

func (m *GetInfo2Responce) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_GetInfo2Responce.Unmarshal(m, b)
}
func (m *GetInfo2Responce) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_GetInfo2Responce.Marshal(b, m, deterministic)
}
func (m *GetInfo2Responce) XXX_Merge(src proto.Message) {
	xxx_messageInfo_GetInfo2Responce.Merge(m, src)
}
func (m *GetInfo2Responce) XXX_Size() int {
	return xxx_messageInfo_GetInfo2Responce.Size(m)
}
func (m *GetInfo2Responce) XXX_DiscardUnknown() {
	xxx_messageInfo_GetInfo2Responce.DiscardUnknown(m)
}

var xxx_messageInfo_GetInfo2Responce proto.InternalMessageInfo

func (m *GetInfo2Responce) GetNeutrino() *NeutrinoInfo {
	if m != nil {
		return m.Neutrino
	}
	return nil
}

func (m *GetInfo2Responce) GetWallet() *WalletInfo {
	if m != nil {
		return m.Wallet
	}
	return nil
}

func (m *GetInfo2Responce) GetLightning() *GetInfoResponse {
	if m != nil {
		return m.Lightning
	}
	return nil
}

func init() {
	proto.RegisterType((*GetInfo2Request)(nil), "lnrpc.GetInfo2Request")
	proto.RegisterType((*NeutrinoBan)(nil), "lnrpc.NeutrinoBan")
	proto.RegisterType((*NeutrinoQuery)(nil), "lnrpc.NeutrinoQuery")
	proto.RegisterType((*NeutrinoInfo)(nil), "lnrpc.NeutrinoInfo")
	proto.RegisterType((*WalletInfo)(nil), "lnrpc.WalletInfo")
	proto.RegisterType((*GetInfo2Responce)(nil), "lnrpc.GetInfo2Responce")
}

func init() { proto.RegisterFile("metaservice.proto", fileDescriptor_b3fb5294949b9545) }

var fileDescriptor_b3fb5294949b9545 = []byte{
	// 644 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0x7c, 0x54, 0xdb, 0x6e, 0xd4, 0x30,
	0x10, 0x55, 0x76, 0xbb, 0x97, 0x4c, 0xba, 0xdd, 0xd6, 0x40, 0x1b, 0x2a, 0x21, 0xaa, 0x15, 0x85,
	0x82, 0x60, 0x2b, 0x2d, 0x97, 0x07, 0x78, 0xab, 0x90, 0x28, 0x48, 0xad, 0x20, 0xad, 0x40, 0xe2,
	0x25, 0xf2, 0x66, 0x87, 0x26, 0x6a, 0xe2, 0x64, 0x6d, 0xa7, 0x55, 0xff, 0x81, 0xcf, 0xe0, 0x87,
	0xf8, 0x1a, 0x5e, 0x91, 0xc7, 0x4e, 0xb7, 0x5d, 0x24, 0x1e, 0x22, 0x79, 0xce, 0x9c, 0x1c, 0xcf,
	0x9c, 0xb1, 0x0d, 0x1b, 0x05, 0x6a, 0xae, 0x50, 0x5e, 0x64, 0x09, 0x8e, 0x2b, 0x59, 0xea, 0x92,
	0x75, 0x72, 0x21, 0xab, 0x64, 0xdb, 0xaf, 0xce, 0xb5, 0x45, 0xb6, 0x7d, 0x59, 0x25, 0x76, 0x39,
	0x3a, 0x82, 0xe1, 0x07, 0xd4, 0x1f, 0xc5, 0x8f, 0x72, 0x12, 0xe1, 0xbc, 0x46, 0xa5, 0xd9, 0x5b,
	0x58, 0x35, 0x71, 0x84, 0xaa, 0x2a, 0x85, 0xc2, 0xd0, 0xdb, 0xf1, 0xf6, 0x82, 0xc9, 0xe6, 0x98,
	0x64, 0xc6, 0x8e, 0xdd, 0x64, 0xa3, 0x5b, 0xdc, 0xd1, 0x29, 0x04, 0xc7, 0x58, 0x6b, 0x99, 0x89,
	0xf2, 0x80, 0x0b, 0xc6, 0x60, 0x85, 0xcf, 0x66, 0x92, 0x24, 0xfc, 0x88, 0xd6, 0x6c, 0x13, 0xba,
	0x12, 0xb9, 0x2a, 0x45, 0xd8, 0x22, 0xd4, 0x45, 0xec, 0x3e, 0xf4, 0x51, 0xcc, 0x62, 0x9d, 0x15,
	0x18, 0xb6, 0x29, 0xd3, 0x43, 0x31, 0x3b, 0xcd, 0x0a, 0x1c, 0xfd, 0xf6, 0x60, 0xd0, 0xc8, 0x7e,
	0xa9, 0x51, 0x5e, 0x19, 0xe1, 0x0a, 0xf1, 0x5a, 0xd8, 0xac, 0x59, 0x08, 0xbd, 0xa4, 0x2c, 0x0a,
	0x2e, 0x66, 0x4e, 0xb9, 0x09, 0xd9, 0x16, 0xf4, 0x24, 0xce, 0x63, 0x51, 0x17, 0xa4, 0x3c, 0x30,
	0x7b, 0xce, 0x8f, 0xeb, 0x82, 0x3d, 0x84, 0x20, 0x91, 0xc8, 0x35, 0xda, 0x6d, 0x57, 0x28, 0x09,
	0x16, 0x32, 0x3b, 0xb3, 0x67, 0xb0, 0x91, 0x73, 0xa5, 0x63, 0x69, 0xbd, 0xb1, 0xb4, 0x0e, 0xd1,
	0x86, 0x26, 0xe1, 0x3c, 0x23, 0xee, 0x73, 0x60, 0x8e, 0x6b, 0xcd, 0xb0, 0xe4, 0x2e, 0x91, 0xd7,
	0x2d, 0xd9, 0x26, 0xa8, 0xa7, 0x9f, 0x2d, 0x58, 0x6d, 0x7a, 0x32, 0x16, 0xb2, 0x5d, 0xe8, 0x98,
	0x36, 0x54, 0xe8, 0xed, 0xb4, 0xf7, 0x82, 0xc9, 0xd0, 0xf9, 0xfd, 0x19, 0x51, 0xbe, 0x47, 0x95,
	0x44, 0x36, 0xcb, 0x1e, 0xc3, 0xca, 0x94, 0x0b, 0x15, 0xb6, 0x88, 0xc5, 0x1c, 0xeb, 0x86, 0xe9,
	0x11, 0xe5, 0xd9, 0x18, 0x7a, 0xf3, 0x1a, 0x65, 0x86, 0x2a, 0x6c, 0x13, 0xf5, 0xee, 0x12, 0x95,
	0x8c, 0x8c, 0x1a, 0x12, 0x7b, 0x00, 0x30, 0xcd, 0xcb, 0xe4, 0x3c, 0x4e, 0xb9, 0x4a, 0xc9, 0x09,
	0x3f, 0xf2, 0x09, 0x39, 0xe4, 0x2a, 0x35, 0x53, 0x4b, 0x31, 0x3b, 0x4b, 0x35, 0x75, 0xdf, 0x89,
	0x5c, 0xc4, 0x9e, 0xc0, 0xd0, 0xfe, 0x66, 0x9a, 0x55, 0x9a, 0x17, 0x15, 0x75, 0xec, 0x47, 0x6b,
	0x04, 0x9f, 0x36, 0xa8, 0xd1, 0xcf, 0x54, 0xac, 0xae, 0x44, 0x92, 0x89, 0xb3, 0xb0, 0xb7, 0xe3,
	0xed, 0xf5, 0x23, 0x3f, 0x53, 0x27, 0x16, 0x18, 0xfd, 0xf1, 0x00, 0xbe, 0xf1, 0x3c, 0xb7, 0xa7,
	0xcb, 0x78, 0x99, 0xd4, 0x52, 0xa2, 0xd0, 0xf1, 0x8d, 0xaa, 0xec, 0xb4, 0xd7, 0x5d, 0xe6, 0xe0,
	0xba, 0xb8, 0x5d, 0x58, 0x6b, 0xd8, 0xae, 0xc8, 0x16, 0x15, 0x39, 0x70, 0xe8, 0xa1, 0xad, 0xf5,
	0x0d, 0x6c, 0xdd, 0x16, 0x5d, 0xd4, 0x6c, 0x0f, 0xdc, 0xbd, 0x9b, 0xca, 0x8b, 0xd2, 0x77, 0x61,
	0xed, 0x92, 0x4a, 0x8b, 0x2f, 0x50, 0xaa, 0xac, 0x14, 0x64, 0x4f, 0x27, 0x1a, 0x58, 0xf4, 0xab,
	0x05, 0xd9, 0x6b, 0x58, 0x75, 0x34, 0xa5, 0xb9, 0x56, 0x64, 0xd4, 0x62, 0x42, 0xb6, 0xb9, 0x13,
	0x93, 0x89, 0x82, 0xcb, 0x45, 0x30, 0xfa, 0xe5, 0xc1, 0xfa, 0xe2, 0x0a, 0x9a, 0x13, 0x92, 0x20,
	0xdb, 0x87, 0xbe, 0x70, 0x73, 0x72, 0xf7, 0xef, 0xce, 0xd2, 0xf8, 0xe8, 0xda, 0x5d, 0x93, 0xd8,
	0x53, 0xe8, 0x5a, 0x51, 0x6a, 0x3d, 0x98, 0x6c, 0xdc, 0xda, 0x96, 0xc8, 0x8e, 0xc0, 0x5e, 0x81,
	0x9f, 0x1b, 0x3f, 0x84, 0x19, 0x44, 0xfb, 0xbf, 0x97, 0x7b, 0x41, 0x9c, 0x7c, 0x82, 0xe0, 0x08,
	0x35, 0x3f, 0xb1, 0x4f, 0x0b, 0x7b, 0x07, 0xfd, 0xa6, 0x68, 0xb6, 0xf4, 0x77, 0xf3, 0x90, 0x6c,
	0x6f, 0xfd, 0x83, 0xdb, 0xee, 0x0e, 0x1e, 0x7d, 0x1f, 0x9d, 0x65, 0x3a, 0xad, 0xa7, 0xe3, 0xa4,
	0x2c, 0xf6, 0xab, 0x73, 0xfd, 0x22, 0xe1, 0x2a, 0x35, 0x8b, 0xd9, 0x7e, 0x2e, 0xcc, 0x27, 0xab,
	0x64, 0xda, 0xa5, 0x17, 0xea, 0xe5, 0xdf, 0x00, 0x00, 0x00, 0xff, 0xff, 0xd8, 0xce, 0x11, 0x03,
	0xd3, 0x04, 0x00, 0x00,
}

// Reference imports to suppress errors if they are not otherwise used.
var _ context.Context
var _ grpc.ClientConn

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
const _ = grpc.SupportPackageIsVersion4

// MetaServiceClient is the client API for MetaService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://godoc.org/google.golang.org/grpc#ClientConn.NewStream.
type MetaServiceClient interface {
	GetInfo2(ctx context.Context, in *GetInfo2Request, opts ...grpc.CallOption) (*GetInfo2Responce, error)
}

type metaServiceClient struct {
	cc *grpc.ClientConn
}

func NewMetaServiceClient(cc *grpc.ClientConn) MetaServiceClient {
	return &metaServiceClient{cc}
}

func (c *metaServiceClient) GetInfo2(ctx context.Context, in *GetInfo2Request, opts ...grpc.CallOption) (*GetInfo2Responce, error) {
	out := new(GetInfo2Responce)
	err := c.cc.Invoke(ctx, "/lnrpc.MetaService/GetInfo2", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// MetaServiceServer is the server API for MetaService service.
type MetaServiceServer interface {
	GetInfo2(context.Context, *GetInfo2Request) (*GetInfo2Responce, error)
}

// UnimplementedMetaServiceServer can be embedded to have forward compatible implementations.
type UnimplementedMetaServiceServer struct {
}

func (*UnimplementedMetaServiceServer) GetInfo2(ctx context.Context, req *GetInfo2Request) (*GetInfo2Responce, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetInfo2 not implemented")
}

func RegisterMetaServiceServer(s *grpc.Server, srv MetaServiceServer) {
	s.RegisterService(&_MetaService_serviceDesc, srv)
}

func _MetaService_GetInfo2_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetInfo2Request)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MetaServiceServer).GetInfo2(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/lnrpc.MetaService/GetInfo2",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MetaServiceServer).GetInfo2(ctx, req.(*GetInfo2Request))
	}
	return interceptor(ctx, in, info, handler)
}

var _MetaService_serviceDesc = grpc.ServiceDesc{
	ServiceName: "lnrpc.MetaService",
	HandlerType: (*MetaServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "GetInfo2",
			Handler:    _MetaService_GetInfo2_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "metaservice.proto",
}
