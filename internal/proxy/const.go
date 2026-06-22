package proxy

const (
	fieldKeyFrom          = "from"
	fieldKeyTo            = "to"
	fieldKeyRule          = "rule"
	fieldKeyDestination   = "destination"
	fieldKeyGateway       = "gateway"
	fieldKeyInterface     = "interface"
	fieldKeyError         = "error"
	cmdIptables           = "iptables"
	cmdSudo               = "sudo"
	argsFlagNAT           = "nat"
	argsFlagDPort         = "--dport"
	argsFlagDNAT          = "DNAT"
	argsFlagToDestination = "--to-destination"

	// Common proxy log field keys.
	fieldKeyService    = "service"
	fieldKeyProtocol   = "protocol"
	fieldKeySupported  = "supported"
	fieldKeyPlatform   = "platform"
	fieldKeyRemoteAddr = "remote_addr"
	fieldKeyRequestID  = "request_id"
	fieldKeyTargetAddr = "target_addr"
	fieldKeyIP         = "ip"

	// Traffic redirection: port ranges.
	portRangeAll = "1:65535"
)
