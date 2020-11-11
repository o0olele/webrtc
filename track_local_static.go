// +build !js

package webrtc

import (
	"sync/atomic"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v3/internal/util"
	"github.com/pion/webrtc/v3/pkg/media"
)

// trackBinding is a single bind for a Track
// Bind can be called multiple times, this stores the
// result for a single bind call so that it can be used when writing
type trackBinding struct {
	ssrc        SSRC
	payloadType PayloadType
	writeStream TrackLocalWriter
}

// TrackLocalStaticRTP  is a TrackLocal that has a pre-set codec and accepts RTP Packets.
// If you wish to send a media.Sample use TrackLocalStaticSample
type TrackLocalStaticRTP struct {
	bindings     []trackBinding
	codec        RTPCodecCapability
	id, streamID string
}

// NewTrackLocalStaticRTP returns a TrackLocalStaticRTP.
func NewTrackLocalStaticRTP(c RTPCodecCapability, id, streamID string) (*TrackLocalStaticRTP, error) {
	return &TrackLocalStaticRTP{
		codec:    c,
		bindings: []trackBinding{},
		id:       id,
		streamID: streamID,
	}, nil
}

// Bind is called by the PeerConnection after negotiation is complete
// This asserts that the code requested is supported by the remote peer.
// If so it setups all the state (SSRC and PayloadType) to have a call
func (s *TrackLocalStaticRTP) Bind(t TrackLocalContext) error {
	for _, codec := range t.CodecParameters() {
		if codec.MimeType == s.codec.MimeType {
			s.bindings = append(s.bindings, trackBinding{
				ssrc:        t.SSRC(),
				payloadType: codec.PayloadType,
				writeStream: t.WriteStream(),
			})
			return nil
		}
	}
	return ErrUnsupportedCodec
}

// Unbind implements the teardown logic when the track is no longer needed. This happens
// because a track has been stopped.
func (s *TrackLocalStaticRTP) Unbind(t TrackLocalContext) error {
	for i := range s.bindings {
		if s.bindings[i].writeStream == t.WriteStream() {
			s.bindings[i] = s.bindings[len(s.bindings)-1]
			s.bindings = s.bindings[:len(s.bindings)-1]
			return nil
		}
	}

	return ErrUnbindFailed
}

// ID is the unique identifier for this Track. This should be unique for the
// stream, but doesn't have to globally unique. A common example would be 'audio' or 'video'
// and StreamID would be 'desktop' or 'webcam'
func (s *TrackLocalStaticRTP) ID() string { return s.id }

// StreamID is the group this track belongs too. This must be unique
func (s *TrackLocalStaticRTP) StreamID() string { return s.streamID }

// Kind controls if this TrackLocal is audio or video
func (s *TrackLocalStaticRTP) Kind() RTPCodecType { return RTPCodecTypeVideo }

// WriteRTP writes a RTP Packet to the TrackLocalStaticRTP
// If one PeerConnection fails the packets will still be sent to
// all PeerConnections. The error message will contain the ID of the failed
// PeerConnections so you can remove them
func (s *TrackLocalStaticRTP) WriteRTP(p *rtp.Packet) error {
	writeErrs := []error{}
	for _, b := range s.bindings {
		p.Header.SSRC = uint32(b.ssrc)
		p.Header.PayloadType = uint8(b.payloadType)
		if _, err := b.writeStream.WriteRTP(&p.Header, p.Payload); err != nil {
			writeErrs = append(writeErrs, err)
		}
	}

	return util.FlattenErrs(writeErrs)
}

// Write writes a RTP Packet as a buffer to the TrackLocalStaticRTP
// If one PeerConnection fails the packets will still be sent to
// all PeerConnections. The error message will contain the ID of the failed
// PeerConnections so you can remove them
func (s *TrackLocalStaticRTP) Write(b []byte) (n int, err error) {
	packet := &rtp.Packet{}
	if err = packet.Unmarshal(b); err != nil {
		return 0, err
	}

	return len(b), s.WriteRTP(packet)
}

// TrackLocalStaticSample is a TrackLocal that has a pre-set codec and accepts Samples.
// If you wish to send a RTP Packet use TrackLocalStaticRTP
type TrackLocalStaticSample struct {
	packetizer atomic.Value // rtp.Packetizer
	rtpTrack   *TrackLocalStaticRTP
}

// NewTrackLocalStaticSample returns a TrackLocalStaticSample
func NewTrackLocalStaticSample(c RTPCodecCapability, id, streamID string) (*TrackLocalStaticSample, error) {
	rtpTrack, err := NewTrackLocalStaticRTP(c, id, streamID)
	if err != nil {
		return nil, err
	}

	return &TrackLocalStaticSample{
		rtpTrack: rtpTrack,
	}, nil
}

// ID is the unique identifier for this Track. This should be unique for the
// stream, but doesn't have to globally unique. A common example would be 'audio' or 'video'
// and StreamID would be 'desktop' or 'webcam'
func (s *TrackLocalStaticSample) ID() string { return s.rtpTrack.ID() }

// StreamID is the group this track belongs too. This must be unique
func (s *TrackLocalStaticSample) StreamID() string { return s.rtpTrack.StreamID() }

// Kind controls if this TrackLocal is audio or video
func (s *TrackLocalStaticSample) Kind() RTPCodecType { return s.rtpTrack.Kind() }

// Bind is called by the PeerConnection after negotiation is complete
// This asserts that the code requested is supported by the remote peer.
// If so it setups all the state (SSRC and PayloadType) to have a call
func (s *TrackLocalStaticSample) Bind(t TrackLocalContext) error {
	if err := s.rtpTrack.Bind(t); err != nil {
		return err
	}

	var payloader rtp.Payloader
	switch s.rtpTrack.codec.MimeType {
	case mimeTypeH264:
		payloader = &codecs.H264Payloader{}
	case mimeTypeOpus:
		payloader = &codecs.OpusPayloader{}
	case mimeTypeVP8:
		payloader = &codecs.VP8Payloader{}
	case mimeTypeVP9:
		payloader = &codecs.VP9Payloader{}
	case mimeTypeG722:
		payloader = &codecs.G722Payloader{}
	case mimeTypePCMU, mimeTypePCMA:
		payloader = &codecs.G711Payloader{}
	default:
		return ErrNoPayloaderForCodec
	}

	s.packetizer.Store(rtp.NewPacketizer(
		rtpOutboundMTU,
		0, // Value is handled when writing
		0, // Value is handled when writing
		payloader,
		rtp.NewRandomSequencer(),
		s.rtpTrack.codec.ClockRate,
	))
	return s.rtpTrack.Bind(t)
}

// Unbind implements the teardown logic when the track is no longer needed. This happens
// because a track has been stopped.
func (s *TrackLocalStaticSample) Unbind(t TrackLocalContext) error {
	return s.rtpTrack.Unbind(t)
}

// WriteSample writes a Sample to the TrackLocalStaticSample
// If one PeerConnection fails the packets will still be sent to
// all PeerConnections. The error message will contain the ID of the failed
// PeerConnections so you can remove them
func (s *TrackLocalStaticSample) WriteSample(sample media.Sample) error {
	p := s.packetizer.Load()
	if p == nil {
		return nil
	}

	samples := sample.Duration.Seconds() * float64(s.rtpTrack.codec.ClockRate)
	packets := p.(rtp.Packetizer).Packetize(sample.Data, uint32(samples))

	writeErrs := []error{}
	for _, p := range packets {
		if err := s.rtpTrack.WriteRTP(p); err != nil {
			writeErrs = append(writeErrs, err)
		}
	}

	return util.FlattenErrs(writeErrs)
}
