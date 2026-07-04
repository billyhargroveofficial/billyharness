package serviceops

import "testing"

func TestManagedServicesReturnsGatewayAndTelegramMetadata(t *testing.T) {
	services := ManagedServices()
	if len(services) != 2 {
		t.Fatalf("services len = %d", len(services))
	}
	want := []ManagedService{
		{Service: GatewayServiceName, Subcommand: GatewaySubcommand, PIDFile: GatewayPIDFile, UnitPath: GatewayUnitPath},
		{Service: TelegramServiceName, Subcommand: TelegramSubcommand, PIDFile: TelegramPIDFile, UnitPath: TelegramUnitPath},
	}
	for i := range want {
		if services[i] != want[i] {
			t.Fatalf("service[%d] = %#v, want %#v", i, services[i], want[i])
		}
	}
	services[0].Service = "mutated.service"
	if got := ManagedServices()[0].Service; got != GatewayServiceName {
		t.Fatalf("ManagedServices should return a copy, got %q", got)
	}
}
