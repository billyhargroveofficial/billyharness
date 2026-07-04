package serviceops

const (
	GatewayServiceName  = "billyharness-gateway.service"
	TelegramServiceName = "billyharness-telegram.service"

	GatewaySubcommand  = "gateway"
	TelegramSubcommand = "telegram"

	GatewayPIDFile  = "gateway.pid"
	TelegramPIDFile = "telegram.pid"

	GatewayUnitPath  = "/etc/systemd/system/" + GatewayServiceName
	TelegramUnitPath = "/etc/systemd/system/" + TelegramServiceName
)

type ManagedService struct {
	Service    string
	Subcommand string
	PIDFile    string
	UnitPath   string
}

func ManagedServices() []ManagedService {
	services := []ManagedService{
		{Service: GatewayServiceName, Subcommand: GatewaySubcommand, PIDFile: GatewayPIDFile, UnitPath: GatewayUnitPath},
		{Service: TelegramServiceName, Subcommand: TelegramSubcommand, PIDFile: TelegramPIDFile, UnitPath: TelegramUnitPath},
	}
	out := make([]ManagedService, len(services))
	copy(out, services)
	return out
}
