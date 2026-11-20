package provider

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sync"
	"testing"

	"github.com/awacino/terraform-provider-awacino/internal/client"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Acceptance tests (TF_ACC=1) run against a real Awacino company - a dedicated test company with an
// API key that has Users and Connections (Read + Write):
//
//	AWACINO_KEY_ID, AWACINO_KEY_SECRET   the key
//	AWACINO_ENDPOINT                     optional, default https://api.awacino.net
//	AWACINO_TEST_DEVICE_ID               an existing Beam-in device of the test company (grants, codes, device lookups)
//
// Every object they create carries the prefix "tfacc-" and is destroyed at the end of its test.
// Tests of the connection API (Beam-in codes, grants, active connections) skip with a message while
// the endpoint answers 404.

func accPreCheck(t *testing.T) {
	t.Helper()
	for _, v := range []string{"AWACINO_KEY_ID", "AWACINO_KEY_SECRET"} {
		if os.Getenv(v) == "" {
			t.Fatalf("%s must be set for acceptance tests", v)
		}
	}
}

func accClient(t *testing.T) *client.Client {
	t.Helper()
	c, err := client.New(client.Config{Endpoint: os.Getenv("AWACINO_ENDPOINT"), KeyID: os.Getenv("AWACINO_KEY_ID"),
		KeySecret: os.Getenv("AWACINO_KEY_SECRET"), UserAgent: "terraform-provider-awacino/acctest"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

var (
	liveMu sync.Mutex
	live   = map[string]error{}
)

// requireEndpoint skips the test while path answers 404 (an endpoint not live yet).
func requireEndpoint(t *testing.T, path string) {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		return // resource.Test skips by itself
	}
	accPreCheck(t)
	liveMu.Lock()
	err, seen := live[path]
	if !seen {
		_, _, err = accClient(t).Do(context.Background(), client.Request{Method: "GET", Path: path, Query: map[string][]string{"limit": {"1"}}}, nil)
		live[path] = err
	}
	liveMu.Unlock()
	if client.IsNotFound(err) {
		t.Skipf("SKIP: the connection API is not live yet - GET %s on %s answered 404. "+
			"This test runs as soon as the endpoint answers.", path, accClient(t).Endpoint())
	}
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
}

func accDevice(t *testing.T) string {
	t.Helper()
	d := os.Getenv("AWACINO_TEST_DEVICE_ID")
	if d == "" && os.Getenv("TF_ACC") != "" {
		t.Skip("SKIP: AWACINO_TEST_DEVICE_ID (a Beam-in device of the test company) is not set")
	}
	return d
}

func accName(kind string) string { return "tfacc-" + kind + "-" + acctest.RandString(8) }

func checkGone(fetch func(c *client.Client, id string) error, typ string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		c, err := client.New(client.Config{Endpoint: os.Getenv("AWACINO_ENDPOINT"), KeyID: os.Getenv("AWACINO_KEY_ID"), KeySecret: os.Getenv("AWACINO_KEY_SECRET")})
		if err != nil {
			return err
		}
		for _, r := range s.RootModule().Resources {
			if r.Type != typ {
				continue
			}
			if err := fetch(c, r.Primary.ID); err == nil {
				return fmt.Errorf("%s %s still exists", typ, r.Primary.ID)
			} else if !client.IsNotFound(err) {
				return err
			}
		}
		return nil
	}
}

func TestAccMember(t *testing.T) {
	ext := accName("member")
	mail := ext + "@example.test"
	cfg := func(name, role string, active bool) string {
		return fmt.Sprintf(`
resource "awacino_member" "m" {
  email          = %q
  display_name   = %q
  external_id    = %q
  role           = %q
  can_automation = true
  active         = %t
}`, mail, name, ext, role, active)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: protoV6,
		CheckDestroy: checkGone(func(c *client.Client, id string) error {
			_, err := c.GetMember(context.Background(), id)
			return err
		}, "awacino_member"),
		Steps: []resource.TestStep{
			{Config: cfg("TF Acceptance", "read", true), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("awacino_member.m", "id"),
				resource.TestCheckResourceAttr("awacino_member.m", "status", "invited"),
				resource.TestCheckResourceAttr("awacino_member.m", "role", "read"),
				resource.TestCheckResourceAttr("awacino_member.m", "can_automation", "true"),
				resource.TestMatchResourceAttr("awacino_member.m", "invitation_code", regexp.MustCompile(`^AWA-[0-9A-Z]{4}-[0-9A-Z]{4}-[0-9A-Z]{4}$`)),
				resource.TestMatchResourceAttr("awacino_member.m", "join_url", regexp.MustCompile(`^https://www\.awacino\.com/join/#AWA-`)),
				resource.TestCheckResourceAttrSet("awacino_member.m", "invitation_expires_at"),
			)},
			// a second plan with the same configuration is empty (the test framework checks it after every step)
			{Config: cfg("TF Acceptance renamed", "user", true), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("awacino_member.m", "display_name", "TF Acceptance renamed"),
				resource.TestCheckResourceAttr("awacino_member.m", "role", "user"),
			)},
			{Config: cfg("TF Acceptance renamed", "user", false), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("awacino_member.m", "active", "false"),
				resource.TestCheckResourceAttr("awacino_member.m", "status", "deactivated"),
			)},
			{Config: cfg("TF Acceptance renamed", "user", true), Check: resource.TestCheckResourceAttr("awacino_member.m", "active", "true")},
			{ResourceName: "awacino_member.m", ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"invitation_code", "join_url", "send_invitation"}},
			{ResourceName: "awacino_member.m", ImportState: true, ImportStateId: "external_id:" + ext, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"invitation_code", "join_url", "send_invitation"}},
		},
	})
}

func TestAccInvitation(t *testing.T) {
	ext := accName("inv")
	cfg := func(round string) string {
		return fmt.Sprintf(`
resource "awacino_member" "m" {
  email       = "%s@example.test"
  external_id = %q
}
resource "awacino_invitation" "i" {
  member_id = awacino_member.m.id
  days      = 3
  triggers  = { round = %q }
}`, ext, ext, round)
	}
	var first string
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{
			{Config: cfg("1"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestMatchResourceAttr("awacino_invitation.i", "code", regexp.MustCompile(`^AWA-`)),
				resource.TestCheckResourceAttr("awacino_invitation.i", "state", "open"),
				resource.TestCheckResourceAttrSet("awacino_invitation.i", "expires_at"),
				func(s *terraform.State) error {
					first = s.RootModule().Resources["awacino_invitation.i"].Primary.Attributes["code"]
					return nil
				},
			)},
			{Config: cfg("2"), Check: func(s *terraform.State) error {
				if s.RootModule().Resources["awacino_invitation.i"].Primary.Attributes["code"] == first {
					return fmt.Errorf("a trigger change must issue a new code")
				}
				return nil
			}},
			{ResourceName: "awacino_invitation.i", ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"code", "join_url", "days", "triggers", "mailed"}},
		},
	})
}

func TestAccBeaminDeviceEnrolment(t *testing.T) {
	label := accName("srv")
	cfg := fmt.Sprintf(`
resource "awacino_beamin_device" "srv" {
  label       = %q
  external_id = %q
  hours       = 1
}`, label, label)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: protoV6,
		CheckDestroy: func(s *terraform.State) error {
			c := accClient(t)
			for _, r := range s.RootModule().Resources {
				if r.Type == "awacino_beamin_device" {
					e, err := c.GetEnrolment(context.Background(), r.Primary.ID)
					if err != nil && !client.IsNotFound(err) {
						return err
					}
					if err == nil && e.State == "open" {
						return fmt.Errorf("enrolment %s still open", r.Primary.ID)
					}
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: cfg, Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestMatchResourceAttr("awacino_beamin_device.srv", "id", regexp.MustCompile(`^enr_[0-9a-f]{16}$`)),
				resource.TestCheckResourceAttr("awacino_beamin_device.srv", "enrolment_state", "open"),
				resource.TestCheckResourceAttr("awacino_beamin_device.srv", "status", "pending"),
				resource.TestCheckResourceAttrSet("awacino_beamin_device.srv", "setup_code"),
				resource.TestMatchResourceAttr("awacino_beamin_device.srv", "cloud_init", regexp.MustCompile(`^#cloud-config`)),
				resource.TestMatchResourceAttr("awacino_beamin_device.srv", "install_docker", regexp.MustCompile(`docker`)),
				resource.TestCheckResourceAttrSet("awacino_beamin_device.srv", "install_apt"),
				resource.TestCheckResourceAttrSet("awacino_beamin_device.srv", "expires_at"),
				resource.TestCheckNoResourceAttr("awacino_beamin_device.srv", "device_id"),
			)},
			{ResourceName: "awacino_beamin_device.srv", ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"setup_code", "install_docker", "install_apt", "cloud_init", "hours", "wait_for_device", "wait_timeout"}},
		},
	})
}

func TestAccBeaminDeviceWaitTimesOut(t *testing.T) {
	label := accName("wait")
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
resource "awacino_beamin_device" "w" {
  label           = %q
  hours           = 1
  wait_for_device = true
  wait_timeout    = "5s"
}`, label),
			ExpectError: regexp.MustCompile(`Timed out waiting for the server to register`),
		}},
	})
}

func TestAccDataSources(t *testing.T) {
	dev := accDevice(t)
	ext := accName("ds")
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
resource "awacino_member" "m" {
  email       = "%s@example.test"
  external_id = %q
}
data "awacino_company" "c" {}
data "awacino_api_key" "k" {}
data "awacino_devices" "beamin" { role = "beamin" }
data "awacino_device" "byid" { id = %q }
data "awacino_device" "byname" { name = data.awacino_device.byid.name }
data "awacino_members" "read" {
  role       = "read"
  depends_on = [awacino_member.m]
}
data "awacino_member" "m" { external_id = awacino_member.m.external_id }
`, ext, ext, dev),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("data.awacino_company.c", "id"),
				resource.TestCheckResourceAttrSet("data.awacino_company.c", "plan"),
				resource.TestCheckResourceAttrSet("data.awacino_company.c", "seats_total"),
				resource.TestCheckTypeSetElemAttr("data.awacino_company.c", "packages.*", "api_access"),
				resource.TestMatchResourceAttr("data.awacino_api_key.k", "id", regexp.MustCompile(`^awk_`)),
				resource.TestCheckResourceAttr("data.awacino_api_key.k", "sections.users", "write"),
				resource.TestCheckResourceAttr("data.awacino_api_key.k", "sections.connections", "write"),
				resource.TestCheckResourceAttr("data.awacino_device.byid", "id", dev),
				resource.TestCheckResourceAttr("data.awacino_device.byid", "role", "beamin"),
				resource.TestCheckResourceAttrPair("data.awacino_device.byname", "id", "data.awacino_device.byid", "id"),
				resource.TestCheckTypeSetElemNestedAttrs("data.awacino_devices.beamin", "devices.*", map[string]string{"id": dev}),
				resource.TestCheckTypeSetElemNestedAttrs("data.awacino_members.read", "members.*", map[string]string{"external_id": ext}),
				resource.TestCheckResourceAttrPair("data.awacino_member.m", "id", "awacino_member.m", "id"),
				resource.TestCheckResourceAttr("data.awacino_member.m", "status", "invited"),
			),
		}},
	})
}

func TestAccAccessGrant(t *testing.T) {
	requireEndpoint(t, "/v1/connections/grants")
	dev := accDevice(t)
	ext := accName("grant")
	cfg := func(until string) string {
		return fmt.Sprintf(`
resource "awacino_member" "m" {
  email       = "%s@example.test"
  external_id = %q
  role        = "read"
}
resource "awacino_access_grant" "g" {
  member_id   = awacino_member.m.id
  device_id   = %q
  reach       = { kind = "network" }
  valid_until = %q
}`, ext, ext, dev, until)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: protoV6,
		CheckDestroy: checkGone(func(c *client.Client, id string) error {
			_, err := c.GetGrant(context.Background(), id)
			return err
		}, "awacino_access_grant"),
		Steps: []resource.TestStep{
			{Config: cfg("2027-06-30T00:00:00Z"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestMatchResourceAttr("awacino_access_grant.g", "id", regexp.MustCompile(`^grt_`)),
				resource.TestCheckResourceAttr("awacino_access_grant.g", "reach.kind", "network"),
				resource.TestCheckResourceAttr("awacino_access_grant.g", "valid_until", "2027-06-30T00:00:00Z"),
			)},
			{Config: cfg("2027-03-31T00:00:00Z"), Check: resource.TestCheckResourceAttr("awacino_access_grant.g", "valid_until", "2027-03-31T00:00:00Z")},
			{ResourceName: "awacino_access_grant.g", ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"valid_until"}},
			{
				// removed outside Terraform (as in the app): drift
				PreConfig: func() {
					c := accClient(t)
					gs, err := c.ListGrants(context.Background(), "", dev)
					if err != nil {
						t.Fatal(err)
					}
					for _, g := range gs {
						_ = c.DeleteGrant(context.Background(), g.ID)
					}
				},
				Config: cfg("2027-03-31T00:00:00Z"), PlanOnly: true, ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestAccBeaminCode(t *testing.T) {
	requireEndpoint(t, "/v1/connections/beamin-codes")
	dev := accDevice(t)
	ext := accName("code")
	cfg := func(label string, paused bool) string {
		return fmt.Sprintf(`
resource "awacino_beamin_code" "c" {
  device_id   = %q
  label       = %q
  external_id = %q
  reach       = { kind = "ports", ports = [22, 443] }
  internet    = "none"
  valid_days  = 30
  paused      = %t
}`, dev, label, ext, paused)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: protoV6,
		CheckDestroy: func(s *terraform.State) error {
			c := accClient(t)
			for _, r := range s.RootModule().Resources {
				if r.Type == "awacino_beamin_code" {
					b, err := c.GetBeaminCode(context.Background(), r.Primary.ID)
					if err == nil && b.State != "revoked" {
						return fmt.Errorf("code %s not revoked", r.Primary.ID)
					}
					if err != nil && !client.IsNotFound(err) {
						return err
					}
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: cfg("tfacc customer - web-01", false), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestMatchResourceAttr("awacino_beamin_code.c", "id", regexp.MustCompile(`^bic_`)),
				resource.TestMatchResourceAttr("awacino_beamin_code.c", "code", regexp.MustCompile(`^[0-9A-Z]{16}$`)),
				resource.TestMatchResourceAttr("awacino_beamin_code.c", "connect_url", regexp.MustCompile(`^https://www\.awacino\.com/beamin/#`)),
				resource.TestCheckResourceAttrSet("awacino_beamin_code.c", "code_hint"),
			)},
			{Config: cfg("tfacc customer - web-01 (paused)", true), Check: resource.TestCheckResourceAttr("awacino_beamin_code.c", "paused", "true")},
			{ResourceName: "awacino_beamin_code.c", ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"code", "connect_url", "valid_days"}},
		},
	})
}

func TestAccActiveConnections(t *testing.T) {
	requireEndpoint(t, "/v1/connections/active")
	dev := accDevice(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`data "awacino_active_connections" "a" { device_id = %q }`, dev),
			Check:  resource.TestCheckResourceAttrSet("data.awacino_active_connections.a", "connections.#"),
		}},
	})
}

// TestAccOnboardOffice applies examples/onboard-office with the real Terraform CLI against the test company (the test
// device as the office device): apply, a second plan without changes, destroy.
func TestAccOnboardOffice(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("acceptance test (TF_ACC=1)")
	}
	requireEndpoint(t, "/v1/connections/grants")
	requireEndpoint(t, "/v1/connections/beamin-codes")
	dev := accDevice(t)
	d, err := accClient(t).GetDevice(context.Background(), dev)
	if err != nil {
		t.Fatal(err)
	}
	pfx := accName("office")
	runExample(t, "onboard-office", exampleEnv{endpoint: os.Getenv("AWACINO_ENDPOINT"), keyID: os.Getenv("AWACINO_KEY_ID"), secret: os.Getenv("AWACINO_KEY_SECRET")},
		map[string]any{
			"office":            pfx,
			"employees":         map[string]any{pfx + "-1": map[string]any{"email": pfx + "-1@example.test", "name": "TF Acceptance 1", "locale": "en"}},
			"office_devices":    []string{d.Name},
			"it_partner_device": d.Name,
		})
}
