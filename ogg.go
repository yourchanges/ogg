// Go implementation of the libogg 1.3.0 container format library. See www.xiph.org/ogg 
// for more info about libogg.
package ogg

// THIS FILE IS PART OF THE OggVorbis SOFTWARE CODEC SOURCE CODE.   
// USE, DISTRIBUTION AND REPRODUCTION OF THIS LIBRARY SOURCE IS     
// GOVERNED BY A BSD-STYLE SOURCE LICENSE INCLUDED WITH THIS SOURCE 
// IN 'COPYING'. PLEASE READ THESE TERMS BEFORE DISTRIBUTING.       
//                                                                 
// THE OggVorbis SOURCE CODE IS (C) COPYRIGHT 1994-2007             
// by the Xiph.Org Foundation http://www.xiph.org/                  

// Go translation done by Gerard vd Schoot

import (
	"bytes"
	"errors"
	"fmt"
)

// Page is used to encapsulate the data in one Ogg bitstream page
type Page struct {
	Header []byte
	Body   []byte
}

// StreamState contains the current encode/decode state of a logical
// Ogg bitstream
type StreamState struct {
	BodyData     []byte  // bytes from packet bodies
	BodyFill     int32   // elements stored; fill mark
	BodyReturned int32   // elements of fill returned
	LacingVals   []int32 // The values that will go to the segment table

	// granulepos values for headers. Not compact this way, 
	// but it is simple coupled to the lacing fifo
	GranuleVals    []int64
	LacingFill     int32
	LacingPacket   int32
	LacingReturned int32

	// working space for header encode
	Header     [282]byte
	HeaderFill int32

	EOS      bool // set when we have buffered the last packet in the logical bitstream
	BOS      bool // set after we've written the initial page of a logical bitstream
	SerialNo int32
	PageNo   int32

	// sequence number for decode; the framing knows where there's a hole
	// in the data, but we need coupling so that the codec (which is in a 
	// separate abstraction layer) also knows about the gap
	PacketNo   int64
	GranulePos int64
}

// packet is used to encapsulate the data and metadata belonging
// to a single raw Ogg/Vorbis packet
type Packet struct {
	Packet []byte
	BOS    int32
	EOS    int32

	GranulePos int64
	PacketNo   int64
}

type SyncState struct {
	Data        []byte
	Fill        int32
	Returned    int32
	unsynced    bool
	HeaderBytes int32
	BodyBytes   int32
}

var dbg bool

func printf(s string, a ...interface{}) {
	if dbg == true {
		fmt.Printf(s, a)
	}
}

// A complete description of Ogg framing exists in docs/framing.html 

func (og *Page) Version() byte {
	return og.Header[4]
}

func (og *Page) Continued() bool {
	return og.Header[5]&0x01 != 0
}

func (og *Page) Bos() bool {
	return og.Header[5]&0x02 != 0
}

func (og *Page) Eos() bool {
	return og.Header[5]&0x04 != 0
}

func (og *Page) GranulePos() int64 {
	page := og.Header
	var granulepos uint64
	granulepos = uint64(page[13] & (0xff))
	granulepos = (granulepos << 8) | uint64(page[12]&0xff)
	granulepos = (granulepos << 8) | uint64(page[11]&0xff)
	granulepos = (granulepos << 8) | uint64(page[10]&0xff)
	granulepos = (granulepos << 8) | uint64(page[9]&0xff)
	granulepos = (granulepos << 8) | uint64(page[8]&0xff)
	granulepos = (granulepos << 8) | uint64(page[7]&0xff)
	granulepos = (granulepos << 8) | uint64(page[6]&0xff)

	return int64(granulepos)
}

func (og *Page) SerialNo() int32 {
	Page := uint32(og.Header[14]) |
		uint32(og.Header[15])<<8 |
		uint32(og.Header[16])<<16 |
		uint32(og.Header[17])<<24
	return int32(Page)
}

func (og *Page) PageNo() int32 {
	Page_pageno := uint32(og.Header[18]) |
		uint32(og.Header[19])<<8 |
		uint32(og.Header[20])<<16 |
		uint32(og.Header[21])<<24
	return int32(Page_pageno)
}

// Packets returns the number of packets that are completed on this page (if
// the leading packet is begun on a previous page, but ends on this
// page, it's counted
//
// NOTE:
// If a page consists of a packet begun on a previous page, and a new
// packet begun (but not completed) on this page, the return will be:
//   Page.Packets()   ==1,
//   Page.Continued() !=0
//
// If a page happens to be a single packet that was begun on a
// previous page, and spans to the next page (in the case of a three or
// more page packet), the return will be:
//   Page.Packets()   ==0,
//   Page.Continued() !=0
func (og *Page) Packets() int {
	var count int

	n := int(og.Header[26])
	for i := 0; i < n; i++ {
		if og.Header[27+i] < 255 {
			count++
		}
	}
	return count
}

var crc_lookup = []uint32{
	0x00000000, 0x04c11db7, 0x09823b6e, 0x0d4326d9,
	0x130476dc, 0x17c56b6b, 0x1a864db2, 0x1e475005,
	0x2608edb8, 0x22c9f00f, 0x2f8ad6d6, 0x2b4bcb61,
	0x350c9b64, 0x31cd86d3, 0x3c8ea00a, 0x384fbdbd,
	0x4c11db70, 0x48d0c6c7, 0x4593e01e, 0x4152fda9,
	0x5f15adac, 0x5bd4b01b, 0x569796c2, 0x52568b75,
	0x6a1936c8, 0x6ed82b7f, 0x639b0da6, 0x675a1011,
	0x791d4014, 0x7ddc5da3, 0x709f7b7a, 0x745e66cd,
	0x9823b6e0, 0x9ce2ab57, 0x91a18d8e, 0x95609039,
	0x8b27c03c, 0x8fe6dd8b, 0x82a5fb52, 0x8664e6e5,
	0xbe2b5b58, 0xbaea46ef, 0xb7a96036, 0xb3687d81,
	0xad2f2d84, 0xa9ee3033, 0xa4ad16ea, 0xa06c0b5d,
	0xd4326d90, 0xd0f37027, 0xddb056fe, 0xd9714b49,
	0xc7361b4c, 0xc3f706fb, 0xceb42022, 0xca753d95,
	0xf23a8028, 0xf6fb9d9f, 0xfbb8bb46, 0xff79a6f1,
	0xe13ef6f4, 0xe5ffeb43, 0xe8bccd9a, 0xec7dd02d,
	0x34867077, 0x30476dc0, 0x3d044b19, 0x39c556ae,
	0x278206ab, 0x23431b1c, 0x2e003dc5, 0x2ac12072,
	0x128e9dcf, 0x164f8078, 0x1b0ca6a1, 0x1fcdbb16,
	0x018aeb13, 0x054bf6a4, 0x0808d07d, 0x0cc9cdca,
	0x7897ab07, 0x7c56b6b0, 0x71159069, 0x75d48dde,
	0x6b93dddb, 0x6f52c06c, 0x6211e6b5, 0x66d0fb02,
	0x5e9f46bf, 0x5a5e5b08, 0x571d7dd1, 0x53dc6066,
	0x4d9b3063, 0x495a2dd4, 0x44190b0d, 0x40d816ba,
	0xaca5c697, 0xa864db20, 0xa527fdf9, 0xa1e6e04e,
	0xbfa1b04b, 0xbb60adfc, 0xb6238b25, 0xb2e29692,
	0x8aad2b2f, 0x8e6c3698, 0x832f1041, 0x87ee0df6,
	0x99a95df3, 0x9d684044, 0x902b669d, 0x94ea7b2a,
	0xe0b41de7, 0xe4750050, 0xe9362689, 0xedf73b3e,
	0xf3b06b3b, 0xf771768c, 0xfa325055, 0xfef34de2,
	0xc6bcf05f, 0xc27dede8, 0xcf3ecb31, 0xcbffd686,
	0xd5b88683, 0xd1799b34, 0xdc3abded, 0xd8fba05a,
	0x690ce0ee, 0x6dcdfd59, 0x608edb80, 0x644fc637,
	0x7a089632, 0x7ec98b85, 0x738aad5c, 0x774bb0eb,
	0x4f040d56, 0x4bc510e1, 0x46863638, 0x42472b8f,
	0x5c007b8a, 0x58c1663d, 0x558240e4, 0x51435d53,
	0x251d3b9e, 0x21dc2629, 0x2c9f00f0, 0x285e1d47,
	0x36194d42, 0x32d850f5, 0x3f9b762c, 0x3b5a6b9b,
	0x0315d626, 0x07d4cb91, 0x0a97ed48, 0x0e56f0ff,
	0x1011a0fa, 0x14d0bd4d, 0x19939b94, 0x1d528623,
	0xf12f560e, 0xf5ee4bb9, 0xf8ad6d60, 0xfc6c70d7,
	0xe22b20d2, 0xe6ea3d65, 0xeba91bbc, 0xef68060b,
	0xd727bbb6, 0xd3e6a601, 0xdea580d8, 0xda649d6f,
	0xc423cd6a, 0xc0e2d0dd, 0xcda1f604, 0xc960ebb3,
	0xbd3e8d7e, 0xb9ff90c9, 0xb4bcb610, 0xb07daba7,
	0xae3afba2, 0xaafbe615, 0xa7b8c0cc, 0xa379dd7b,
	0x9b3660c6, 0x9ff77d71, 0x92b45ba8, 0x9675461f,
	0x8832161a, 0x8cf30bad, 0x81b02d74, 0x857130c3,
	0x5d8a9099, 0x594b8d2e, 0x5408abf7, 0x50c9b640,
	0x4e8ee645, 0x4a4ffbf2, 0x470cdd2b, 0x43cdc09c,
	0x7b827d21, 0x7f436096, 0x7200464f, 0x76c15bf8,
	0x68860bfd, 0x6c47164a, 0x61043093, 0x65c52d24,
	0x119b4be9, 0x155a565e, 0x18197087, 0x1cd86d30,
	0x029f3d35, 0x065e2082, 0x0b1d065b, 0x0fdc1bec,
	0x3793a651, 0x3352bbe6, 0x3e119d3f, 0x3ad08088,
	0x2497d08d, 0x2056cd3a, 0x2d15ebe3, 0x29d4f654,
	0xc5a92679, 0xc1683bce, 0xcc2b1d17, 0xc8ea00a0,
	0xd6ad50a5, 0xd26c4d12, 0xdf2f6bcb, 0xdbee767c,
	0xe3a1cbc1, 0xe760d676, 0xea23f0af, 0xeee2ed18,
	0xf0a5bd1d, 0xf464a0aa, 0xf9278673, 0xfde69bc4,
	0x89b8fd09, 0x8d79e0be, 0x803ac667, 0x84fbdbd0,
	0x9abc8bd5, 0x9e7d9662, 0x933eb0bb, 0x97ffad0c,
	0xafb010b1, 0xab710d06, 0xa6322bdf, 0xa2f33668,
	0xbcb4666d, 0xb8757bda, 0xb5365d03, 0xb1f740b4}

// Init the encode/decode logical stream state
func (ot *StreamState) Init(serialNo int32) int {
	if ot != nil {
		BodyStorage := 16 * 1024
		LacingStorage := 1024

		ot.BodyData = make([]byte, BodyStorage)
		ot.LacingVals = make([]int32, LacingStorage)
		ot.GranuleVals = make([]int64, LacingStorage)

		ot.SerialNo = serialNo

		return 0
	}
	return -1
}

// Check async/delayed error detection for the StreamState
func (ot *StreamState) Check() bool {
	if ot == nil || ot.BodyData == nil {
		return false
	}
	return true
}

// Clear does not free ot, only the non-flat storage within
func (ot *StreamState) Clear() int {
	ot = nil
	return 0
}

// Helpers for Stream_encode; this keeps the structure and
// what's happening fairly clear
func (ot *StreamState) bodyExpand(needed int) {
	if len(ot.BodyData) <= int(ot.BodyFill)+needed {
		ot.BodyData = append(ot.BodyData, make([]byte, needed+1024)...)
	}
}

func (ot *StreamState) lacingExpand(needed int) {
	if len(ot.LacingVals) <= int(ot.LacingFill)+needed {
		ot.LacingVals = append(ot.LacingVals, make([]int32, needed+32)...)
		ot.GranuleVals = append(ot.GranuleVals, make([]int64, needed+32)...)
	}
}

// ChecksumSet the page 
// Direct table CRC; note that this will be faster in the future if we
// perform the checksum simultaneously with other copies
func (og *Page) ChecksumSet() {
	if og != nil {
		var crc_reg uint32

		// safety; needed for API behavior, but not framing code 
		og.Header[22] = 0
		og.Header[23] = 0
		og.Header[24] = 0
		og.Header[25] = 0

		for i := range og.Header {
			crc_reg = (crc_reg << 8) ^ crc_lookup[((crc_reg>>24)&0xff)^uint32(og.Header[i])]
		}
		for i := range og.Body {
			crc_reg = (crc_reg << 8) ^ crc_lookup[((crc_reg>>24)&0xff)^uint32(og.Body[i])]
		}

		og.Header[22] = byte(crc_reg) & 0xff
		og.Header[23] = byte(crc_reg>>8) & 0xff
		og.Header[24] = byte(crc_reg>>16) & 0xff
		og.Header[25] = byte(crc_reg>>24) & 0xff
	}
}

// IovecIn submit data to the internal buffer of the framing engine
func (ot *StreamState) IovecIn(iov [][]byte, count int, EOS int32, granulepos int64) error {
	var Bytes, lacing_vals, i int

	if ot.Check() == false {
		return errors.New("func Check() returned false in func IovecIn()")
	}
	if iov == nil {
		return nil
	}

	for i = 0; i < count; i++ {
		Bytes += len(iov[i])
	}
	lacing_vals = Bytes/255 + 1

	if ot.BodyReturned != 0 {
		// advance packet data according to the body_returned pointer. We had
		// to keep it around to return a pointer into the buffer last call
		ot.BodyFill -= ot.BodyReturned
		if ot.BodyFill != 0 {
			t := ot.BodyReturned
			u := t + ot.BodyFill
			copy(ot.BodyData, ot.BodyData[t:u])
		}
		ot.BodyReturned = 0
	}

	// make sure we have the buffer storage 
	ot.bodyExpand(Bytes)
	ot.lacingExpand(lacing_vals)

	// Copy in the submitted packet.  Yes, the copy is a waste; this is
	// the liability of overly clean abstraction for the time being.  It
	// will actually be fairly easy to eliminate the extra copy in the
	// future 
	for i = 0; i < count; i++ {
		copy(ot.BodyData[ot.BodyFill:], iov[i])
		ot.BodyFill += int32(len(iov[i]))
	}

	// Store lacing vals for this packet 
	for i = 0; i < lacing_vals-1; i++ {
		ot.LacingVals[int(ot.LacingFill)+i] = 255
		ot.GranuleVals[int(ot.LacingFill)+i] = ot.GranulePos
	}
	ot.LacingVals[int(ot.LacingFill)+i] = int32(Bytes % 255)
	ot.GranuleVals[int(ot.LacingFill)+i] = granulepos
	ot.GranulePos = granulepos

	// flag the first segment as the beginning of the packet 
	ot.LacingVals[ot.LacingFill] |= 0x100

	ot.LacingFill += int32(lacing_vals)

	// for the sake of completeness 
	ot.PacketNo++

	if EOS != 0 {
		ot.EOS = true
	}

	return nil
}

func (ot *StreamState) PacketIn(op *Packet) error {
	return ot.IovecIn([][]byte{op.Packet}, 1, op.EOS, op.GranulePos)
}

// Conditionally flush a page; force==0 will only flush nominal-size
// pages, force==1 forces us to flush a page regardless of page size
// so long as there's any data available at all.
func (ot *StreamState) flushI(og *Page, force bool, nfill int) int {
	var bodyBytes int32
	var i, acc, vals, maxvals int32
	var granule_pos int64 = -1

	if ot.LacingFill > 255 {
		maxvals = 255
	} else {
		maxvals = ot.LacingFill
	}

	if ot.Check() == false {
		return 0
	}
	if maxvals == 0 {
		return 0
	}

	// construct a page 
	// decide how many segments to include 

	// If this is the initial header case, the first page must only include
	// the initial header packet 
	if ot.BOS == false { // 'initial header page' case 
		granule_pos = 0
		for vals = 0; vals < maxvals; vals++ {
			if (ot.LacingVals[vals] & 0x0ff) < 255 {
				vals++
				break
			}
		}
	} else {

		// The extra packets_done, packet_just_done logic here attempts to do two things:
		// 1) Don't unneccessarily span pages.
		// 2) Unless necessary, don't flush pages if there are less than four packets on
		//    them; this expands page size to reduce unneccessary overhead if incoming packets
		//    are large.
		// These are not necessary behaviors, just 'always better than naive flushing'
		// without requiring an application to explicitly request a specific optimized
		// behavior. We'll want an explicit behavior setup pathway eventually as well. 

		packets_done := 0
		packet_just_done := 0
		for vals = 0; vals < maxvals; vals++ {
			if acc > int32(nfill) && packet_just_done >= 4 {
				force = true
				break
			}
			acc += int32(ot.LacingVals[vals] & 0x0ff)
			if (ot.LacingVals[vals] & 0xff) < 255 {
				granule_pos = ot.GranuleVals[vals]
				packets_done++
				packet_just_done = packets_done
			} else {
				packet_just_done = 0
			}
		}
		if vals == 255 {
			force = true
		}
	}

	if force == false {
		return 0
	}

	// construct the header in temp storage 
	copy(ot.Header[0:4], "OggS")

	// stream structure version 
	ot.Header[4] = 0x00

	// continued packet flag? 
	ot.Header[5] = 0x00
	if (ot.LacingVals[0] & 0x100) == 0 {
		ot.Header[5] |= 0x01
	}
	// first page flag? 
	if ot.BOS == false {
		ot.Header[5] |= 0x02
	}
	// last page flag? 
	if ot.EOS && ot.LacingFill == int32(vals) {
		ot.Header[5] |= 0x04
	}
	ot.BOS = true

	// 64 bits of PCM position 
	for i = 6; i < 14; i++ {
		ot.Header[i] = byte(granule_pos) & 0xff
		granule_pos >>= 8
	}

	// 32 bits of stream serial number 
	serialno := ot.SerialNo
	for i = 14; i < 18; i++ {
		ot.Header[i] = byte(serialno) & 0xff
		serialno >>= 8
	}

	// 32 bits of page counter (we have both counter and page header
	// because this val can roll over) 
	if ot.PageNo == -1 {
		ot.PageNo = 0
	} // because someone called stream_reset; this would be a
	// strange thing to do in an encode stream, but it has
	// plausible uses 

	pageno := ot.PageNo
	ot.PageNo++

	for i = 18; i < 22; i++ {
		ot.Header[i] = byte(pageno) & 0xff
		pageno >>= 8
	}

	// zero for computation; filled in later 
	ot.Header[22] = 0
	ot.Header[23] = 0
	ot.Header[24] = 0
	ot.Header[25] = 0

	// segment table 
	ot.Header[26] = byte(vals) & 0xff
	for i = 0; i < vals; i++ {
		ot.Header[i+27] = byte(ot.LacingVals[i]) & 0xff
		bodyBytes += int32(ot.Header[i+27])
	}

	// set pointers in the Page struct 
	og.Header = ot.Header[0 : vals+27]
	ot.HeaderFill = vals + 27
	og.Body = ot.BodyData[ot.BodyReturned : ot.BodyReturned+bodyBytes]

	// advance the lacing data and set the body_returned pointer 
	ot.LacingFill -= int32(vals)
	copy(ot.LacingVals, ot.LacingVals[vals:vals+ot.LacingFill])
	copy(ot.GranuleVals, ot.GranuleVals[vals:vals+ot.LacingFill])
	ot.BodyReturned += bodyBytes

	// calculate the checksum 
	og.ChecksumSet()

	// done 
	return 1
}

// Flush will flush remaining packets into a page (returning nonzero),
// even if there is not enough data to trigger a flush normally
// (undersized page). If there are no packets or partial packets to
// flush, Stream_flush returns 0.  Note that Stream_flush will
// try to flush a normal sized page like Pageout; a call to
// Flush does not guarantee that all packets have flushed.
// Only a return value of 0 from Stream_flush indicates all packet
// data is flushed into pages.
//
// Since Flush will flush the last page in a stream even if
// it's undersized, you almost certainly want to use PageOut
// (and *not* Flush) unless you specifically need to flush
// a page regardless of size in the middle of a stream.
func (ot *StreamState) Flush(og *Page) int {
	return ot.flushI(og, true, 4096)
}

// FlushFill, like Flush, but an argument is provided to adjust the nominal
// page size for applications which are smart enough to provide their
// own delay based flushing
func (ot *StreamState) FlushFill(og *Page, nfill int) int {
	return ot.flushI(og, true, nfill)
}

// PageOut constructs pages from buffered packet segments.  The pointers
// returned are to static buffers; do not free. The returned buffers are
// good only until the next call (using the same StreamState).
func (ot *StreamState) PageOut(og *Page) int {
	var force bool

	if ot.Check() == false {
		return 0
	}

	if (ot.EOS && ot.LacingFill != 0) || // 'were done, now flush' case
		(ot.LacingFill != 0 && !ot.BOS) { // 'initial header page' case 
		force = true
	}

	return ot.flushI(og, force, 4096)
}

// PageOutFill, like PageOut, but an argument is provided to adjust the nominal
// page size for applications which are smart enough to provide their
// own delay based flushing.
func (ot *StreamState) PageOutFill(og *Page, nfill int) int {
	var force bool
	if ot.Check() == false {
		return 0
	}

	if (ot.EOS && ot.LacingFill == 1) || // 'were done, now flush' case 
		(ot.LacingFill == 1 && !ot.BOS) { // 'initial header page' case 
		force = true
	}

	return ot.flushI(og, force, nfill)
}

func (ot *StreamState) Eos() bool {
	if ot.Check() == false {
		return true
	}
	return ot.EOS
}

// DECODING PRIMITIVES: packet streaming layer
//
// This has two layers to place more of the multi-serialno and paging
// control in the application's hands.  First, we expose a data buffer
// using Buffer().  The app either copies into the
// buffer, or passes it directly to read(), etc.  We then call
// Wrote() to tell how many_bytes we just added.
//
// Pages are returned (pointers into the buffer in SyncState)
// by Sync_pageout().  The page is then submitted to
// PageIn() along with the appropriate
// StreamState* (ie, matching serialno).  We then get raw
// packets out calling Stream_packetout() with a
// StreamState. 

// Clear non-flat storage within
func (oy *SyncState) Clear() int {
	if oy != nil {
		oy.Data = nil
		oy.Fill = 0
		oy.Returned = 0
		oy.unsynced = false
		oy.HeaderBytes = 0
		oy.BodyBytes = 0
	}
	return 0
}

func (oy *SyncState) Buffer(size int) []byte {
	// first, clear out any space that has been previously returned
	if oy.Returned != 0 {
		oy.Fill -= oy.Returned
		if oy.Fill > 0 {
			copy(oy.Data, oy.Data[oy.Returned:oy.Returned+oy.Fill])
		}
		oy.Returned = 0
	}

	if size > len(oy.Data)-int(oy.Fill) {
		// We need to extend the internal buffer
		// an extra page to be nice
		oy.Data = append(oy.Data, make([]byte, size+4096)...)
	}

	// expose a segment at least as large as requested at the fill mark
	return oy.Data[oy.Fill:]
}

func (oy *SyncState) Wrote(Bytes int) int {
	if int(oy.Fill)+Bytes > len(oy.Data) {
		return -1
	}
	oy.Fill += int32(Bytes)
	return 0
}

// PageSeek sync the stream. This is meant to be useful for finding page
// boundaries.
//
// return values for this:
//  -n) skipped n_bytes
//  0) page not ready; more data (no_bytes skipped)
//  n) page synced at current location; page length n_bytes
func (oy *SyncState) PageSeek(og *Page) int32 {
	page := oy.Data[oy.Returned:oy.Fill]
	Bytes := len(page)

	printf("oy.Data = %v ", page[:4])
	if oy.HeaderBytes == 0 {
		var headerbytes int
		if Bytes < 27 { // not enough for a header
			return 0
		}

		// verify capture pattern
		if bytes.Equal(page[0:4], []byte("OggS")) == false {
			goto sync_fail
		}

		headerbytes = int(page[26]) + 27
		if Bytes < headerbytes { // not enough for header + seg table
			return 0
		}

		// count up body length in the segment table

		for j := 0; j < int(page[26]); j++ {
			oy.BodyBytes += int32(page[27+j])
		}
		oy.HeaderBytes = int32(headerbytes)
	}

	if oy.BodyBytes+oy.HeaderBytes > int32(Bytes) {
		return 0
	}

	// The whole test page is buffered.  Verify the checksum
	{
		// Grab the checksum_bytes, set the header field to zero
		var chksum [4]byte
		var log Page

		copy(chksum[0:], page[22:22+4])
		page[22] = 0
		page[23] = 0
		page[24] = 0
		page[25] = 0

		// set up a temp page struct and recompute the checksum
		log.Header = page[0:oy.HeaderBytes]
		log.Body = page[oy.HeaderBytes : oy.HeaderBytes+oy.BodyBytes]
		log.ChecksumSet()

		// Compare 
		if bytes.Equal(chksum[0:4], page[22:26]) == false {
			// D'oh. Mismatch! Corrupt page (or miscapture and not a page at all)
			// replace the computed checksum with the one actually read in
			copy(page[22:], chksum[0:4])

			// Bad checksum. Lose sync
			goto sync_fail
		}
	}

	// yes, have a whole page all ready to go
	{
		if og != nil {
			og.Header = page[0:oy.HeaderBytes]
			og.Body = page[oy.HeaderBytes : oy.HeaderBytes+oy.BodyBytes]
		}

		oy.unsynced = false
		size := oy.HeaderBytes + oy.BodyBytes
		oy.Returned += size
		oy.HeaderBytes = 0
		oy.BodyBytes = 0
		return size
	}

sync_fail:

	oy.HeaderBytes = 0
	oy.BodyBytes = 0

	// search for possible capture
	// check at offset of 1
	next := int32(bytes.Index(page[1:], []byte("OggS")))
	if next == -1 {
		next = oy.Fill - oy.Returned
		oy.Returned = oy.Fill
	} else {
		next++ // compensate offset of 1
		oy.Returned += next
	}
	return -next // skipped "next" bytes. So negative
}

// sync the stream and get a page.  Keep trying until we find a page.
// Suppress 'sync errors' after reporting the first.
//
// return values:
//  -1) recapture (hole in data)
//  0) need more data
//  1) page returned
//
// Returns pointers into buffered data; invalidated by next call to
// _stream, _clear, _init, or _buffer
//
func (oy *SyncState) PageOut(og *Page) int {

	// all we need to do is verify a page at the head of the stream
	// buffer. If it doesn't verify, we look for the next potential frame

	for {
		ret := oy.PageSeek(og)
		if ret > 0 {
			// have a page 
			return 1
		}
		if ret == 0 {
			// need more data 
			return 0
		}

		// head did not start a synced page... skipped some_bytes
		if oy.unsynced == false {
			oy.unsynced = true
			return -1
		}

		// loop. keep looking 

	}

	return -1 // You shouldn't be here anyway
}

// PageIn adds the incoming page to the stream state; we decompose the page
// into packet segments here as well.
func (ot *StreamState) PageIn(og *Page) error {
	header := og.Header
	body := og.Body
	var segptr int

	version := og.Version()
	continued := og.Continued()
	bos := og.Bos()
	eos := og.Eos()
	granulepos := og.GranulePos()
	pageno := og.PageNo()
	segments := int(header[26])

	if ot.Check() == false {
		return errors.New("func Check() returned false in func PageIn()")
	}

	// clean up 'returned data' 
	{
		lr := ot.LacingReturned
		br := ot.BodyReturned

		// body data 
		if br != 0 {
			ot.BodyFill -= br
			if ot.BodyFill != 0 {
				copy(ot.BodyData, ot.BodyData[br:br+ot.BodyFill])
			}
			ot.BodyReturned = 0
		}

		if lr == 1 {
			// segment table 
			if (ot.LacingFill - lr) != 0 {
				copy(ot.LacingVals, ot.LacingVals[lr:lr+ot.LacingFill])
				copy(ot.GranuleVals, ot.GranuleVals[lr:lr+ot.LacingFill])
			}
			ot.LacingFill -= lr
			ot.LacingPacket -= lr
			ot.LacingReturned = 0
		}
	}

	// check the serial number 
	if ot.SerialNo != og.SerialNo() {
		return errors.New("Serial numbers don't match in func PageIn()")
	}
	if version > 0 {
		return errors.New("Version > 0 in func PageIn()")
	}

	ot.lacingExpand(segments + 1)

	// are we in sequence? 
	if pageno != ot.PageNo {
		var i int32

		// unroll previous partial packet (if any) 
		for i = ot.LacingPacket; i < ot.LacingFill; i++ {
			ot.BodyFill -= int32(ot.LacingVals[i]) & 0xff
		}
		ot.LacingFill = ot.LacingPacket

		// make a note of dropped data in segment table 
		if ot.PageNo != -1 {
			ot.LacingVals[ot.LacingFill] = 0x400
			ot.LacingFill++
			ot.LacingPacket++
		}
	}

	// are we a 'continued packet' page?  If so, we may need to skip
	// some segments 
	if continued == true {
		if ot.LacingFill < 1 || ot.LacingVals[ot.LacingFill-1] == 0x400 {
			bos = false
			for ; segptr < segments; segptr++ {
				val := int(header[27+segptr])
				body = body[val:]
				if val < 255 {
					segptr++
					break
				}
			}
		}
	}

	if len(body) != 0 {
		ot.bodyExpand(len(body))
		copy(ot.BodyData[ot.BodyFill:], body)
		ot.BodyFill += int32(len(body))
	}

	{
		saved := -1
		for segptr < segments {
			val := int32(header[27+segptr])
			ot.LacingVals[ot.LacingFill] = val
			ot.GranuleVals[ot.LacingFill] = -1

			if bos == true {
				ot.LacingVals[ot.LacingFill] |= 0x100
				bos = false
			}

			if val < 255 {
				saved = int(ot.LacingFill)
			}

			ot.LacingFill++
			segptr++

			if val < 255 {
				ot.LacingPacket = ot.LacingFill
			}
		}

		// set the granulepos on the last granuleval of the last full packet 
		if saved != -1 {
			ot.GranuleVals[saved] = granulepos
		}

	}

	if eos == true {
		ot.EOS = true
		if ot.LacingFill > 0 {
			ot.LacingVals[ot.LacingFill-1] |= 0x200
		}
	}

	ot.PageNo = pageno + 1

	return nil
}

// Reset clears things to an initial state.  Good to call, eg, before seeking
func (oy *SyncState) Reset() {
	oy.Data = nil
	oy.Fill = 0
	oy.Returned = 0
	oy.unsynced = false
	oy.HeaderBytes = 0
	oy.BodyBytes = 0
}

func (ot *StreamState) Reset() int {
	if ot.Check() == false {
		return -1
	}

	ot.BodyFill = 0
	ot.BodyReturned = 0

	ot.LacingFill = 0
	ot.LacingPacket = 0
	ot.LacingReturned = 0

	ot.HeaderFill = 0

	ot.EOS = false
	ot.BOS = false
	ot.PageNo = -1
	ot.PacketNo = 0
	ot.GranulePos = 0

	return 0
}

func (ot *StreamState) ResetSerialNo(serialno int) int {
	if ot.Check() == false {
		return -1
	}
	ot.Reset()
	ot.SerialNo = int32(serialno)
	return 0
}

// The last part of decode. We have the stream broken into packet
// segments.  Now we need to group them into packets (or return the
// out of sync markers)
func (ot *StreamState) packetOut(op *Packet, adv bool) int {

	ptr := ot.LacingReturned

	if ot.LacingPacket <= ptr {
		return 0
	}

	// we need to tell the codec there's a gap; it might need to
	// handle previous packet dependencies. 
	if (ot.LacingVals[ptr] & 0x400) != 0 {
		ot.LacingReturned++
		ot.PacketNo++
		return -1
	}

	// just using peek as an inexpensive way
	// to ask if there's a whole packet waiting 
	if op == nil && adv == false {
		return 1
	}

	// Gather the whole packet. We'll have no holes or a partial packet 
	{
		size := int(ot.LacingVals[ptr] & 0xff)
		Bytes := size
		eos := int(ot.LacingVals[ptr] & 0x200) // last packet of the stream?
		bos := int(ot.LacingVals[ptr] & 0x100) // first packet of the stream?

		for size == 255 {
			ptr++
			val := int(ot.LacingVals[ptr])
			size = val & 0xff
			if (val & 0x200) != 0 {
				eos = 0x200
			}
			Bytes += size
		}

		if op != nil {
			op.EOS = int32(eos)
			op.BOS = int32(bos)
			op.Packet = ot.BodyData[ot.BodyReturned : ot.BodyReturned+int32(Bytes)]
			op.PacketNo = ot.PacketNo
			op.GranulePos = ot.GranuleVals[ptr]
		}

		if adv {
			ot.BodyReturned += int32(Bytes)
			ot.LacingReturned = ptr + 1
			ot.PacketNo++
		}
	}
	return 1
}

func (ot *StreamState) PacketOut(op *Packet) int {
	if ot.Check() == false {
		return 0
	}
	return ot.packetOut(op, true)
}

func (ot *StreamState) PacketPeek(op *Packet) int {
	if ot.Check() == false {
		return 0
	}
	return ot.packetOut(op, false)
}
