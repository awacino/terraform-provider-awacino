package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

var protoV6 = map[string]func() (tfprotov6.ProviderServer, error){
	"awacino": providerserver.NewProtocol6WithError(New("test")()),
}

// Unit tests: Terraform runs against mockAPI (no network, no TF_ACC needed).

func TestUnitMemberLifecycle(t *testing.T) {
	m := newMockAPI(t)
	cfg := func(name, role string, active bool) string {
		return m.providerConfig() + fmt.Sprintf(`
resource "awacino_member" "dieter" {
  email        = "Dieter@Company.example"
  display_name = %q
  external_id  = "4711"
  role         = %q
  active       = %t
}`, name, role, active)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		CheckDestroy: func(*terraform.State) error {
			if len(m.members) != 0 {
				return fmt.Errorf("%d members left", len(m.members))
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: cfg("Dieter Muster", "read", true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("awacino_member.dieter", "id"),
					resource.TestCheckResourceAttr("awacino_member.dieter", "email", "Dieter@Company.example"),
					resource.TestCheckResourceAttr("awacino_member.dieter", "status", "invited"),
					resource.TestCheckResourceAttr("awacino_member.dieter", "role", "read"),
					resource.TestCheckResourceAttr("awacino_member.dieter", "can_automation", "false"),
					resource.TestMatchResourceAttr("awacino_member.dieter", "invitation_code", regexp.MustCompile(`^AWA-`)),
					resource.TestMatchResourceAttr("awacino_member.dieter", "join_url", regexp.MustCompile(`^https://www\.awacino\.com/join/#AWA-`)),
					resource.TestCheckResourceAttrSet("awacino_member.dieter", "invitation_expires_at"),
				),
			},
			{
				Config: cfg("Dieter M.", "user", false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("awacino_member.dieter", "display_name", "Dieter M."),
					resource.TestCheckResourceAttr("awacino_member.dieter", "role", "user"),
					resource.TestCheckResourceAttr("awacino_member.dieter", "active", "false"),
					resource.TestCheckResourceAttr("awacino_member.dieter", "status", "deactivated"),
					// the first code stays in state, it is never read back
					resource.TestMatchResourceAttr("awacino_member.dieter", "invitation_code", regexp.MustCompile(`^AWA-`)),
				),
			},
			{
				ResourceName: "awacino_member.dieter", ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"invitation_code", "join_url", "email", "send_invitation"},
			},
			{
				ResourceName: "awacino_member.dieter", ImportState: true, ImportStateId: "external_id:4711", ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"invitation_code", "join_url", "email", "send_invitation"},
			},
		},
	})
}

func TestUnitMemberEmailChangeReplaces(t *testing.T) {
	m := newMockAPI(t)
	cfg := func(mail string) string {
		return m.providerConfig() + fmt.Sprintf(`
resource "awacino_member" "x" {
  email = %q
  send_invitation = { days = 3, locale = "de" }
}`, mail)
	}
	var first string
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{
			{Config: cfg("a@x.example"), Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("awacino_member.x", "role", "read"),
				resource.TestCheckResourceAttr("awacino_member.x", "send_invitation.days", "3"),
				func(s *terraform.State) error {
					first = s.RootModule().Resources["awacino_member.x"].Primary.ID
					return nil
				},
			)},
			// the same address in other case: no change
			{Config: cfg("A@X.example"), PlanOnly: true},
			{Config: cfg("b@x.example"), Check: func(s *terraform.State) error {
				if s.RootModule().Resources["awacino_member.x"].Primary.ID == first {
					return fmt.Errorf("a new address must replace the member")
				}
				if len(m.members) != 1 {
					return fmt.Errorf("%d members, want 1 (old one offboarded)", len(m.members))
				}
				return nil
			}},
		},
	})
}

func TestUnitInvitation(t *testing.T) {
	m := newMockAPI(t)
	cfg := func(trigger string) string {
		return m.providerConfig() + fmt.Sprintf(`
resource "awacino_member" "anna" {
  email = "anna@it-partner.example"
}
resource "awacino_invitation" "anna" {
  member_id = awacino_member.anna.id
  days      = 3
  triggers  = { round = %q }
}`, trigger)
	}
	var code string
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{
			{Config: cfg("1"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestMatchResourceAttr("awacino_invitation.anna", "code", regexp.MustCompile(`^AWA-`)),
				resource.TestCheckResourceAttr("awacino_invitation.anna", "state", "open"),
				resource.TestCheckResourceAttr("awacino_invitation.anna", "mailed", "false"),
				resource.TestCheckResourceAttrPair("awacino_invitation.anna", "member_id", "awacino_member.anna", "id"),
				func(s *terraform.State) error {
					code = s.RootModule().Resources["awacino_invitation.anna"].Primary.Attributes["code"]
					return nil
				},
			)},
			{Config: cfg("2"), Check: func(s *terraform.State) error {
				if s.RootModule().Resources["awacino_invitation.anna"].Primary.Attributes["code"] == code {
					return fmt.Errorf("a trigger change must issue a new code")
				}
				return nil
			}},
			{
				ResourceName: "awacino_invitation.anna", ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"code", "join_url", "days", "triggers", "mailed"},
			},
		},
	})
}

func TestUnitInvitationRevokedOutsideIsReissued(t *testing.T) {
	m := newMockAPI(t)
	cfg := m.providerConfig() + `
resource "awacino_member" "a" { email = "a@x.example" }
resource "awacino_invitation" "a" { member_id = awacino_member.a.id }`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				PreConfig: func() {
					m.mu.Lock()
					m.invitations = map[string]map[string]any{}
					m.mu.Unlock()
				},
				Config: cfg, PlanOnly: true, ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestUnitBeaminDeviceWaitsAndRenames(t *testing.T) {
	m := newMockAPI(t)
	m.registerNext = true
	cfg := func(name string) string {
		return m.providerConfig() + fmt.Sprintf(`
resource "awacino_beamin_device" "web02" {
  label           = "web-02.example.com"
  external_id     = "cust4711-web02"
  hours           = 1
  wait_for_device = true
  wait_timeout    = "1m"
  name            = %q
}`, name)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		CheckDestroy: func(*terraform.State) error {
			for id, d := range m.devices {
				if id != "srv-1" {
					return fmt.Errorf("device %v left", d["name"])
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: cfg("web-02"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestMatchResourceAttr("awacino_beamin_device.web02", "id", regexp.MustCompile(`^enr_`)),
				resource.TestCheckResourceAttr("awacino_beamin_device.web02", "enrolment_state", "used"),
				resource.TestCheckResourceAttrSet("awacino_beamin_device.web02", "device_id"),
				resource.TestCheckResourceAttr("awacino_beamin_device.web02", "name", "web-02"),
				resource.TestCheckResourceAttr("awacino_beamin_device.web02", "status", "active"),
				resource.TestMatchResourceAttr("awacino_beamin_device.web02", "cloud_init", regexp.MustCompile(`^#cloud-config`)),
				resource.TestCheckResourceAttrSet("awacino_beamin_device.web02", "setup_code"),
			)},
			{Config: cfg("web-02-renamed"), Check: resource.TestCheckResourceAttr("awacino_beamin_device.web02", "name", "web-02-renamed")},
			{
				ResourceName: "awacino_beamin_device.web02", ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"setup_code", "install_docker", "install_apt", "cloud_init", "hours", "wait_for_device", "wait_timeout"},
			},
		},
	})
}

func TestUnitBeaminDeviceWithoutWait(t *testing.T) {
	m := newMockAPI(t)
	cfg := m.providerConfig() + `
resource "awacino_beamin_device" "x" {
  label = "pending-01"
}`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		CheckDestroy: func(*terraform.State) error {
			for _, e := range m.enrolments {
				if e["state"] != "revoked" {
					return fmt.Errorf("enrolment %v not revoked", e["id"])
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: cfg, Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("awacino_beamin_device.x", "enrolment_state", "open"),
				resource.TestCheckResourceAttr("awacino_beamin_device.x", "status", "pending"),
				resource.TestCheckResourceAttr("awacino_beamin_device.x", "name", "pending-01"),
				resource.TestCheckNoResourceAttr("awacino_beamin_device.x", "device_id"),
				resource.TestCheckResourceAttr("awacino_beamin_device.x", "hours", "2"),
			)},
			{Config: cfg, PlanOnly: true},
		},
	})
}

func TestUnitAccessGrantAndDrift(t *testing.T) {
	m := newMockAPI(t)
	cfg := func(until string) string {
		return m.providerConfig() + fmt.Sprintf(`
resource "awacino_member" "anna" {
  email = "anna@it-partner.example"
}
resource "awacino_access_grant" "nas" {
  member_id   = awacino_member.anna.id
  device_id   = "srv-1"
  reach       = { kind = "network" }
  valid_until = %q
}`, until)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		CheckDestroy: func(*terraform.State) error {
			if len(m.grants) != 0 {
				return fmt.Errorf("%d grants left", len(m.grants))
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: cfg("2027-06-30T00:00:00Z"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestMatchResourceAttr("awacino_access_grant.nas", "id", regexp.MustCompile(`^grt_`)),
				resource.TestCheckResourceAttr("awacino_access_grant.nas", "reach.kind", "network"),
				resource.TestCheckNoResourceAttr("awacino_access_grant.nas", "internet"),
				// the server answers +00:00; the configured spelling stays (no perpetual diff)
				resource.TestCheckResourceAttr("awacino_access_grant.nas", "valid_until", "2027-06-30T00:00:00Z"),
				resource.TestCheckResourceAttr("awacino_access_grant.nas", "state", "active"),
			)},
			{Config: cfg("2027-03-31T00:00:00Z"), Check: resource.TestCheckResourceAttr("awacino_access_grant.nas", "valid_until", "2027-03-31T00:00:00Z")},
			{ResourceName: "awacino_access_grant.nas", ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"valid_until"}},
			{
				// a grant removed in the app shows as drift
				PreConfig: func() { m.mu.Lock(); m.grants = map[string]map[string]any{}; m.mu.Unlock() },
				Config:    cfg("2027-03-31T00:00:00Z"), PlanOnly: true, ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestUnitBeaminCode(t *testing.T) {
	m := newMockAPI(t)
	cfg := func(label string, paused bool, ports string) string {
		return m.providerConfig() + fmt.Sprintf(`
resource "awacino_beamin_code" "customer" {
  device_id   = "srv-1"
  label       = %q
  external_id = "cust4711-web01-code"
  reach       = { kind = "ports", ports = [%s] }
  valid_days  = 30
  paused      = %t
}
output "url" {
  value     = awacino_beamin_code.customer.connect_url
  sensitive = true
}`, label, ports, paused)
	}
	var first, firstCode string
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{
			{Config: cfg("Customer 4711 - web-01", false, "22, 443"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestMatchResourceAttr("awacino_beamin_code.customer", "id", regexp.MustCompile(`^bic_`)),
				resource.TestMatchResourceAttr("awacino_beamin_code.customer", "code", regexp.MustCompile(`^[0-9A-Z]{16}$`)),
				resource.TestMatchResourceAttr("awacino_beamin_code.customer", "connect_url", regexp.MustCompile(`^https://www\.awacino\.com/beamin/#`)),
				resource.TestCheckResourceAttr("awacino_beamin_code.customer", "reach.ports.#", "2"),
				resource.TestCheckResourceAttr("awacino_beamin_code.customer", "state", "active"),
				resource.TestCheckResourceAttrSet("awacino_beamin_code.customer", "expires_at"),
				func(s *terraform.State) error {
					a := s.RootModule().Resources["awacino_beamin_code.customer"].Primary
					first, firstCode = a.ID, a.Attributes["code"]
					return nil
				},
			)},
			// label + pause: in place, the code stays
			{Config: cfg("Customer 4711 - web-01 (paused)", true, "22, 443"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("awacino_beamin_code.customer", "state", "paused"),
				resource.TestCheckResourceAttr("awacino_beamin_code.customer", "paused_reason", "admin"),
				func(s *terraform.State) error {
					a := s.RootModule().Resources["awacino_beamin_code.customer"].Primary
					if a.ID != first || a.Attributes["code"] != firstCode {
						return fmt.Errorf("label / pause must not make a new code")
					}
					return nil
				},
			)},
			// reach: a new code
			{Config: cfg("Customer 4711 - web-01 (paused)", true, "22"), Check: func(s *terraform.State) error {
				if s.RootModule().Resources["awacino_beamin_code.customer"].Primary.ID == first {
					return fmt.Errorf("a reach change must make a new code")
				}
				if len(m.codes) != 1 {
					return fmt.Errorf("%d codes, the old one must be revoked", len(m.codes))
				}
				return nil
			}},
			{
				ResourceName: "awacino_beamin_code.customer", ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"code", "connect_url", "valid_days"},
			},
		},
	})
}

func TestUnitConnectionAPINotLive(t *testing.T) {
	m := newMockAPI(t)
	m.noConnAPI = true
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{{
			Config: m.providerConfig() + `
resource "awacino_beamin_code" "c" {
  device_id = "srv-1"
  reach     = { kind = "network" }
}`,
			ExpectError: regexp.MustCompile(`(?s)not available.*connection API`),
		}},
	})
}

func TestUnitMissingSectionNamesTheCheckbox(t *testing.T) {
	m := newMockAPI(t)
	m.scopes = []string{"users:read", "users:write"}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{{
			Config:      m.providerConfig() + `resource "awacino_beamin_device" "x" { label = "x" }`,
			ExpectError: regexp.MustCompile(`no Connections section with Write access`),
		}},
	})
}

func TestUnitValidation(t *testing.T) {
	m := newMockAPI(t)
	for name, c := range map[string]struct{ cfg, err string }{
		"admin role": {`resource "awacino_member" "x" {
  email = "a@b.example"
  role  = "admin"
}`, `value must be one of`},
		"rights need read": {`resource "awacino_member" "x" {
  email       = "a@b.example"
  role        = "user"
  can_agentic = false
}`, `Only for role read`},
		"ports needed": {`resource "awacino_beamin_code" "x" {
  device_id = "d"
  reach     = { kind = "ports" }
}`, `needs a ports list`},
		"no ports": {`resource "awacino_access_grant" "x" {
  member_id = "1"
  device_id = "d"
  reach     = { kind = "network", ports = [22] }
}`, `only used with`},
		"bad port": {`resource "awacino_beamin_code" "x" {
  device_id = "d"
  reach     = { kind = "ports", ports = [0] }
}`, `between 1 and 65535`},
		"both ends": {`resource "awacino_beamin_code" "x" {
  device_id  = "d"
  reach      = { kind = "network" }
  valid_days = 3
  expires_at = "2027-01-01T00:00:00Z"
}`, `cannot be specified when`},
		"bad duration": {`resource "awacino_beamin_device" "x" {
  label        = "x"
  wait_timeout = "soon"
}`, `Invalid duration`},
		"bad time": {`resource "awacino_access_grant" "x" {
  member_id   = "1"
  device_id   = "d"
  reach       = { kind = "network" }
  valid_until = "tomorrow"
}`, `Invalid time`},
		"two lookups": {`data "awacino_device" "x" {
  id   = "a"
  name = "b"
}`, `only one\) of \[id,name,external_id\]`},
	} {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6,
				Steps:                    []resource.TestStep{{Config: m.providerConfig() + c.cfg, ExpectError: regexp.MustCompile(c.err)}},
			})
		})
	}
}

func TestUnitBadSecret(t *testing.T) {
	m := newMockAPI(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
provider "awacino" {
  endpoint   = %q
  key_id     = "awk_test0000000000000000000000"
  key_secret = "wrong"
}
data "awacino_company" "c" {}`, m.srv.URL),
			ExpectError: regexp.MustCompile(`key id or secret is wrong`),
		}},
	})
}

func TestUnitDataSources(t *testing.T) {
	m := newMockAPI(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{{
			Config: m.providerConfig() + `
resource "awacino_member" "a" {
  email       = "a@x.example"
  external_id = "tech-017"
}
data "awacino_company" "c" {}
data "awacino_api_key" "k" {}
data "awacino_devices" "beamin" { role = "beamin" }
data "awacino_device" "web" { name = "WEB-01" }
data "awacino_members" "all" { depends_on = [awacino_member.a] }
data "awacino_member" "a" { external_id = awacino_member.a.external_id }
data "awacino_active_connections" "web" { device_id = data.awacino_device.web.id }
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.awacino_company.c", "plan", "premium"),
				resource.TestCheckResourceAttr("data.awacino_company.c", "seats_total", "20"),
				resource.TestCheckResourceAttr("data.awacino_company.c", "grants_available", "true"),
				resource.TestCheckTypeSetElemAttr("data.awacino_company.c", "access_capabilities.*", "network"),
				resource.TestCheckResourceAttr("data.awacino_api_key.k", "sections.connections", "write"),
				resource.TestCheckResourceAttr("data.awacino_api_key.k", "sections.audit", "none"),
				resource.TestCheckResourceAttr("data.awacino_devices.beamin", "devices.#", "1"),
				resource.TestCheckResourceAttr("data.awacino_devices.beamin", "devices.0.kind", "unattended"),
				resource.TestCheckResourceAttr("data.awacino_device.web", "id", "srv-1"),
				resource.TestCheckResourceAttr("data.awacino_device.web", "name", "WEB-01"),
				resource.TestCheckResourceAttr("data.awacino_members.all", "members.#", "1"),
				resource.TestCheckResourceAttrPair("data.awacino_member.a", "id", "awacino_member.a", "id"),
				resource.TestCheckResourceAttr("data.awacino_active_connections.web", "connections.0.via", "member"),
				resource.TestCheckResourceAttr("data.awacino_active_connections.web", "connections.0.member_id", "101"),
			),
		}},
	})
}

func TestUnitProviderNeedsCredentials(t *testing.T) {
	t.Setenv("AWACINO_KEY_ID", "")
	t.Setenv("AWACINO_KEY_SECRET", "")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{{
			Config:      `data "awacino_company" "c" {}`,
			ExpectError: regexp.MustCompile(`Missing Awacino API key id`),
		}},
	})
}

func TestUnitTokenSharedAcrossRuns(t *testing.T) {
	m := newMockAPI(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{
			{Config: m.providerConfig() + `data "awacino_company" "c" {}`},
			{Config: m.providerConfig() + `data "awacino_api_key" "k" {}`},
		},
	})
	if m.tokenCalls != 1 {
		t.Fatalf("%d token requests for several Terraform runs, want 1", m.tokenCalls)
	}
}

// The onboard-office example through the real Terraform CLI (dev_overrides build of this provider) against the mock:
// apply, a second plan without changes, destroy.
func TestUnitOnboardOfficeExampleCLI(t *testing.T) {
	m := newMockAPI(t)
	runExample(t, "onboard-office", exampleEnv{endpoint: m.srv.URL, keyID: "awk_test0000000000000000000000", secret: "aws_test"},
		map[string]any{"office_devices": []string{"web-01"}, "it_partner_device": "web-01"})
	if len(m.members) != 0 || len(m.grants) != 0 || len(m.codes) != 0 {
		t.Fatalf("left after destroy: members=%d grants=%d codes=%d", len(m.members), len(m.grants), len(m.codes))
	}
}

// The hoster pattern: the device data source waits until the server registered.
func TestUnitDeviceDataSourceWaits(t *testing.T) {
	m := newMockAPI(t)
	m.lateDevice = "late-web-01"
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{
			{
				Config: m.providerConfig() + `
data "awacino_device" "late" {
  name         = "late-web-01"
  wait_timeout = "1m"
}`,
				Check: resource.TestCheckResourceAttr("data.awacino_device.late", "id", "late-1"),
			},
			{
				Config: m.providerConfig() + `
data "awacino_device" "missing" {
  name = "nowhere"
}`,
				ExpectError: regexp.MustCompile(`No device matches`),
			},
		},
	})
}

// The managed-service-provider example through the Terraform CLI against the mock.
func TestUnitManagedServiceProviderExampleCLI(t *testing.T) {
	m := newMockAPI(t)
	for _, n := range []string{"Customer 12 - NAS", "Customer 12 - Server"} {
		m.devices[n] = map[string]any{"id": n, "name": n, "role": "beamin", "kind": "unattended", "status": "active", "platform": "linux"}
	}
	runExample(t, "managed-service-provider", exampleEnv{endpoint: m.srv.URL, keyID: "awk_test0000000000000000000000", secret: "aws_test"}, map[string]any{})
	if len(m.members) != 0 || len(m.grants) != 0 {
		t.Fatalf("left after destroy: members=%d grants=%d", len(m.members), len(m.grants))
	}
}
