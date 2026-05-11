# ADR 2: License Strategy Review (SKL-1.0)

## Status: Proposed

## Context

Saker currently ships under **Saker Source License Version 1.0 (SKL-1.0)**
— Apache 2.0 base + three additional terms:

1. **Usage Limit** — production use requires *both* (a) annual gross revenue ≤
   1,000,000 CNY (~$140,000 USD) **AND** (b) registered users ≤ 100
2. **Attribution Requirement** — "Powered by Saker.cc" in UI + docs
3. **No Automatic Conversion** — license never auto-relicenses to OSI

When either limit is exceeded, a commercial license must be obtained.

### Real-world impact of the current thresholds

| Adopter type | Status under SKL-1.0 |
| --- | --- |
| Solo dev personal project | ✅ Free (eval/personal) |
| 5-person startup, 50 paying users, 600k CNY ARR | ✅ Free |
| Internal company tool, 0 revenue, 200 employees | ❌ Trips user cap |
| 30k-MAU community forum, ad-supported, 800k CNY rev | ❌ Trips user cap |
| Mid-size SaaS, 5M CNY ARR, 80 power-user accounts | ❌ Trips revenue cap |
| OSS project funded by donations, 50k users | ❌ Trips user cap |

The conjunction "must satisfy **BOTH**" + "Or **EITHER** triggers
commercial" wording (LICENSE §1) means the **user cap is the real
bottleneck** for most non-revenue use cases. 100 users is reached by
almost any internal SaaS or community tool.

### Why this matters for adoption

OSS / source-available adoption follows a funnel:

1. Discover → 2. Evaluate → 3. Self-host → 4. Production → 5. Pay

The current 100-user cap kills the funnel between (3) and (4) for most
non-startup users. Mid-market companies exploring Saker for an internal
agent platform see the cap and rule it out before reaching (5).

Comparable projects' thresholds:

| Project | Revenue cap | User cap | Trigger logic |
| --- | --- | --- | --- |
| Saker (current) | 1M CNY | 100 | OR |
| Mattermost (formerly) | $5M | none | revenue only |
| Plausible | none | none | offered alongside AGPL |
| Bitwarden | none | none | AGPL + paid features |
| Sentry | $1M | none | BSL → Apache after 4 yrs |
| BSL (HashiCorp) | none | none | non-compete clause + auto-conversion |

Saker is conservative in two dimensions where peers are conservative in
one.

## Decision

We propose three discrete adjustment options, ordered from
least-disruptive to most-disruptive. **No decision yet** — this ADR
captures the analysis to inform a stakeholder choice.

### Option A — Loosen thresholds, keep SKL-1.0 structure

Smallest legal change; pure dial-twist.

| Term | Current | Proposed |
| --- | --- | --- |
| Revenue cap | 1M CNY (~$140k) | 5M CNY (~$700k) |
| User cap | 100 | 10,000 |
| Trigger logic | OR | OR (unchanged) |

**Pros**: One LICENSE edit + version bump (SKL-1.1); existing
contributors already CLA'd to a near-identical license.

**Cons**: Still source-available, not OSI-approved → blocked by some
corporate procurement; still creates ambiguity for non-revenue
deployments at scale.

### Option B — BSL with auto-conversion (Sentry / HashiCorp model)

Switch to **Business Source License 1.1** with these parameters:

- **Change Date**: each release auto-relicenses to Apache 2.0 after 4
  years
- **Additional Use Grant**: production use permitted unless the user is
  building a hosted Saker-compatible service ("non-compete")
- **Attribution**: keep "Powered by Saker.cc" via separate trademark
  policy

**Pros**: Removes user/revenue cap entirely → unblocks mid-market
adoption; commercial moat preserved via non-compete + trademark; clear
path to fully open code over time.

**Cons**: BSL is not OSS by OSI definition; some communities still
refuse it; the 4-year window means current code is permissive forever
once aged out.

### Option C — Open-core / AGPL + dual license

Relicense the core to **AGPL-3.0** and keep premium features
(SkillHub, multi-tenant orchestration, advanced monitoring) under
proprietary license.

**Pros**: AGPL is OSI-approved → unblocks corporate procurement; clear
"free for self-host, paid for SaaS" mental model.

**Cons**: AGPL § 13 requires source disclosure for SaaS use → may push
adopters away from AGPL'd modules; need to draw a clean core/premium
boundary in code (significant refactor); cannot easily reverse.

## Trade-off summary

| Dimension | A (loose SKL) | B (BSL) | C (AGPL+open-core) |
| --- | --- | --- | --- |
| Effort | 1 day | 1 week | 2-4 weeks |
| Adoption ceiling | Medium | High | High |
| Procurement-friendly | No | Mixed | Yes |
| Commercial moat | Strong | Strong | Strong |
| Reversibility | Easy | Hard | Very hard |
| Community signal | Same | Worse w/ purists | Mixed (SaaS aversion) |

## Recommendation (for discussion, not final)

**Option A** as a stop-gap (within 2 weeks), then revisit once we have
6 months of adoption data:

- If procurement blockage dominates the funnel → **Option C**
- If hosted-clone competition dominates → **Option B**
- If both stay manageable → keep **Option A**

Whatever path is chosen, the **attribution requirement** ("Powered by
Saker.cc") should remain — it is the cheapest, least adoption-blocking
moat we have.

## Open questions

1. Do we have CLA infrastructure to relicense external contributions?
   (If not, a relicense forces re-signing.)
2. What is the actual conversion rate from "evaluate" to "pay" today?
   Without that number, optimizing the funnel is guesswork.
3. Are there contracted customers whose SLA assumes today's license
   text? They must be notified before any change.

## Consequences if no change is made

- Mid-market adoption stays capped by the 100-user threshold.
- Internal-tool category effectively requires commercial license, but
  this is not advertised — friction risk.
- Commercial license inquiries remain the main signal (high noise,
  small numbers).

## References

- LICENSE (current SKL-1.0 text)
- README.md §许可证 / Licence sections
- Comparable license analyses: Sentry (BSL→Apache), Plausible (AGPL),
  HashiCorp (BSL transition writeups)
