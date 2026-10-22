package tool

import "testing"

// TestAllowPIILocksTheOptIn guards the security-relevant contract: a tool that
// opts into unmasked PII must be visible to the executor, and a merge must not
// silently drop the opt-in.
func TestAllowPIILocksTheOptIn(t *testing.T) {
	if (Policy{}).AllowsPII() {
		t.Fatal("the zero policy must not allow PII")
	}
	if !(Policy{}).AllowPII().AllowsPII() {
		t.Fatal("AllowPII must be visible to AllowsPII")
	}
	if !(Policy{}).Merge(Policy{}.AllowPII()).AllowsPII() {
		t.Fatal("Merge must preserve the PII opt-in")
	}
	if !(Policy{}).AllowPII().Merge(Policy{}).AllowsPII() {
		t.Fatal("Merge must not clear an explicit PII opt-in")
	}
}

// TestMergeDoesNotDowngradeEgress guards the policy that a later Write() or
// Read() option cannot drop the egress allowlist by overwriting the effect.
func TestMergeDoesNotDowngradeEgress(t *testing.T) {
	egress := Policy{}.WithEffect(EffectEgress).Hosts("api.example.com")
	write := Policy{}.WithEffect(EffectWrite)
	for name, merged := range map[string]Policy{
		"write after egress": egress.Merge(write),
		"read after egress":  egress.Merge(Policy{}),
		"egress after write": write.Merge(egress),
	} {
		if merged.Effect() != EffectEgress {
			t.Errorf("%s: effect = %v, want egress", name, merged.Effect())
		}
		if len(merged.EgressHosts()) == 0 || merged.EgressHosts()[0] != "api.example.com" {
			t.Errorf("%s: hosts = %v, want api.example.com", name, merged.EgressHosts())
		}
	}
}
