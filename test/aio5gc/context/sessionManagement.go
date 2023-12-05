/**
 * SPDX-License-Identifier: Apache-2.0
 * © Copyright 2023 Hewlett Packard Enterprise Development LP
 */
package context

import (
	"errors"
	"my5G-RANTester/internal/common/tools"
	"net"
	"slices"

	"github.com/free5gc/nas"
	"github.com/free5gc/nas/nasConvert"
	"github.com/free5gc/nas/nasMessage"
	"github.com/free5gc/openapi/models"
	"github.com/mohae/deepcopy"
	log "github.com/sirupsen/logrus"
)

type SmContext struct {
	// pdu session information
	pduSessionID                 int32
	snssai                       models.Snssai
	pduAddress                   net.IP
	dataNetwork                  DataNetwork
	userLocation                 models.NrLocation
	plmnID                       models.PlmnId
	pti                          uint8
	sessionType                  uint8
	ProtocolConfigurationOptions *ProtocolConfigurationOptions
	sessionRule                  *models.SessionRule
	defQosQFI                    uint8
}

type ProtocolConfigurationOptions struct {
	DNSIPv4Request     bool
	DNSIPv6Request     bool
	PCSCFIPv4Request   bool
	IPv4LinkMTURequest bool
}

func NewSmContext(pduSessionID int32) *SmContext {
	c := &SmContext{pduSessionID: pduSessionID}
	c.ProtocolConfigurationOptions = &ProtocolConfigurationOptions{}
	return c
}

func (c *SmContext) GetPduSessionId() int32 {
	return c.pduSessionID
}

func (c *SmContext) SetSnssai(snssai models.Snssai) {
	c.snssai = snssai
}

func (c *SmContext) SetPDUAddress(ip net.IP) {
	c.pduAddress = ip
}

func (c *SmContext) GetSnnsai() models.Snssai {
	return c.snssai
}

func (c *SmContext) SetDataNetwork(dn DataNetwork) {
	c.dataNetwork = dn
}

func (c *SmContext) GetDataNetwork() DataNetwork {
	return c.dataNetwork
}

func (c *SmContext) SetUserLocation(location models.NrLocation) {
	c.userLocation = location
}

func (c *SmContext) SetPti(pti uint8) {
	c.pti = pti
}

func (c *SmContext) GetPti() uint8 {
	return c.pti
}

func (c *SmContext) SetPduSessionType(sType uint8) {
	c.sessionType = sType
}

func (c *SmContext) GetPduSessionType() uint8 {
	return c.sessionType
}

func (c *SmContext) GetSessionRule() *models.SessionRule {
	return c.sessionRule
}

func (c *SmContext) SetSessionRule(sessionRule *models.SessionRule) {
	c.sessionRule = sessionRule
}

func (c *SmContext) GetDefQosQFI() uint8 {
	return c.defQosQFI
}

func (c *SmContext) SetDefQosQFI(defQosQFI uint8) {
	c.defQosQFI = defQosQFI
}

func (smContext *SmContext) PDUAddressToNAS() ([12]byte, uint8) {
	var addr [12]byte
	var addrLen uint8
	copy(addr[:], smContext.pduAddress)
	switch smContext.sessionType {
	case nasMessage.PDUSessionTypeIPv4:
		addrLen = 4 + 1
	case nasMessage.PDUSessionTypeIPv6:
	case nasMessage.PDUSessionTypeIPv4IPv6:
		addrLen = 12 + 1
	}
	return addr, addrLen
}

func CreatePDUSession(ulNasTransport *nasMessage.ULNASTransport,
	ue *UEContext,
	fgc *Aio5gc,
	pduSessionID int32,
	smMessage []uint8,
) (smContext *SmContext, err error) {
	session := fgc.session

	var (
		snssai models.Snssai
		dnn    string
	)
	// If the S-NSSAI IE is not included, select a default snssai
	if ulNasTransport.SNSSAI != nil {
		snssai = nasConvert.SnssaiToModels(ulNasTransport.SNSSAI)
	} else {
		snssai = ue.GetNssai()
	}

	dnnList := session.GetDnnList()
	if ulNasTransport.DNN != nil {
		if !slices.Contains(dnnList, ulNasTransport.DNN.GetDNN()) {
			return nil, errors.New("[5GC] Unknown DNN requested")
		}
		dnn = ulNasTransport.DNN.GetDNN()

	} else {
		dnn = dnnList[0]
	}

	newSmContext := NewSmContext(pduSessionID)
	newSmContext.SetSnssai(snssai)
	dn, err := session.GetDataNetwork(dnn)
	if err != nil {
		return nil, err
	}
	newSmContext.SetDataNetwork(dn)

	locationCopy := deepcopy.Copy(*ue.GetUserLocationInfo()).(models.NrLocation)
	newSmContext.SetUserLocation(locationCopy)

	n1smContent := ulNasTransport.PayloadContainer.GetPayloadContainerContents()
	m := nas.NewMessage()
	err = m.GsmMessageDecode(&n1smContent)
	if err != nil {
		return nil, errors.New("[5GC][NAS] GsmMessageDecode Error: " + err.Error())
	}
	if m.GsmHeader.GetMessageType() != nas.MsgTypePDUSessionEstablishmentRequest {
		return nil, errors.New("[5GC][NAS] UL NAS Transport container message expected to be PDU Session Establishment Request but was not")
	}
	sessionRequest := m.PDUSessionEstablishmentRequest

	newSmContext.SetPti(sessionRequest.GetPTI())
	newSmContext.SetPduSessionType(sessionRequest.GetPDUSessionTypeValue())
	newSmContext.SetSessionRule(session.GetSessionRules()[0])
	newSmContext.SetDefQosQFI(uint8(1))
	ip, err := tools.IncrementIP(session.lastAllocatedIP.String(), "10.0.0.0/8")
	if err != nil {
		log.Fatal("[5GC][NAS] Error while allocating ip for PDU session: " + err.Error())
	}

	session.lastAllocatedIP = net.ParseIP(ip)
	newSmContext.SetPDUAddress(session.lastAllocatedIP)
	EPCOContents := sessionRequest.ExtendedProtocolConfigurationOptions.GetExtendedProtocolConfigurationOptionsContents()
	protocolConfigurationOptions := nasConvert.NewProtocolConfigurationOptions()
	err = protocolConfigurationOptions.UnMarshal(EPCOContents)
	if err != nil {
		return nil, errors.New("[5GC][NAS] Error while decoding protocol configuration options : " + err.Error())
	}
	for _, container := range protocolConfigurationOptions.ProtocolOrContainerList {
		switch container.ProtocolOrContainerID {
		case nasMessage.DNSServerIPv6AddressRequestUL:
			newSmContext.ProtocolConfigurationOptions.DNSIPv6Request = true
		case nasMessage.PCSCFIPv4AddressRequestUL:
			newSmContext.ProtocolConfigurationOptions.PCSCFIPv4Request = true
		case nasMessage.DNSServerIPv4AddressRequestUL:
			newSmContext.ProtocolConfigurationOptions.DNSIPv4Request = true
		case nasMessage.IPv4LinkMTURequestUL:
			newSmContext.ProtocolConfigurationOptions.IPv4LinkMTURequest = true
		}
	}

	sessionRequest.GetExtendedProtocolConfigurationOptionsContents()

	ue.AddSmContext(newSmContext)
	log.Infof("[5GC] Create smContext[pduSessionID: %d] Success", pduSessionID)
	return newSmContext, nil
}
