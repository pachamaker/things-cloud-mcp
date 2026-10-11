# Legal and Terms-of-Service Risk Notes

> **Not legal advice.** General information compiled on 2026-10-10 from public
> sources, for the maintainer's own risk assessment. Clause wording is paraphrased;
> check the linked originals before relying on them. Consult a lawyer for decisions
> with real stakes.

## 1. Summary

| Question | Short answer |
|---|---|
| Is reverse-engineering the protocol itself illegal? | Unlikely to be the problem. Observing your own app's traffic and reimplementing a protocol is broadly permitted in the EU/Germany and Russia; protocols and file formats are not protected expression. |
| Does using it break Cultured Code's terms? | Very likely yes. The Things Cloud Terms prohibit access other than through publicly supported interfaces and prohibit reverse engineering except where law allows it. |
| What is the realistic sanction? | Suspension or termination of the Things Cloud account (allowed without notice), and self-inflicted data loss that Cultured Code disclaims. |
| Lawsuits / criminal exposure for personal use? | Very low / negligible. No takedowns, cease-and-desist letters or lawsuits against Things Cloud clients were found in about nine years of public projects. |
| Publishing the fork on GitHub? | Low to moderate risk with honest branding and disclaimers. |
| Running it as a hosted service for other people? | Different category: third-party credentials, GDPR and Russian 152-FZ duties, revocation risk. **Out of scope for this project.** |

## 2. Cultured Code's position

### 2.1 Terms of Service
[culturedcode.com/terms](https://culturedcode.com/terms/) (last modified 2012):

- Prohibits accessing the service "by any means other than our publicly supported
  interfaces" (misuse section).
- Prohibits reverse engineering and decompiling, except where applicable law expressly
  permits it.
- Prohibits circumventing security or authentication measures and accessing non-public
  areas.
- You are responsible for everything done with your password and account.
- Access may be suspended or terminated for non-compliance, or with or without cause.
- No responsibility for deleted or corrupted content; service "as is"; liability for
  data loss excluded to the extent the law allows; you indemnify Cultured Code for
  third-party claims from your breaches.
- EU customers: German law, courts in Stuttgart. The terms do not say which law applies
  to non-EU customers (e.g. a Russian resident).

### 2.2 Public statements
- FAQ "Does Things provide an API?"
  ([article 2967034](https://culturedcode.com/things/support/articles/2967034/)): no
  public API; integrations that access the database or Things Cloud directly are
  described as dangerous and a cause of data corruption and loss.
- "Third-Party AI Tools and Things", updated 2026-04-02
  ([article 5510170](https://culturedcode.com/things/support/articles/5510170/)): any AI
  tool that asks for your Things Cloud credentials is described as not safe; the safe
  methods listed are the URL scheme, Shortcuts, AppleScript (Mac) and Mail to Things.
  **This project matches the "not safe" description exactly.**

### 2.3 Enforcement history found
- No GitHub DMCA notices mentioning Cultured Code or Things (search of `github/dmca`;
  coverage not guaranteed).
- Unofficial clients have existed publicly since ~2017 (`nicolai86/things-cloud-sdk`),
  including a hosted multi-user service (`wbopan/things-cloud-mcp`). Tolerance so far is
  weak evidence of non-enforcement, not permission. The original MCP repository was
  archived on 2026-09-27 without a stated reason.

## 3. Legal framework (condensed)

| Jurisdiction | Rule | Relevance |
|---|---|---|
| EU | Directive 2009/24/EC Art. 1(2) (interfaces' ideas unprotected), Art. 5(3) (observe/study/test), Art. 6 (decompilation for interoperability), Art. 8 (contrary terms void) | Studying traffic of your own app is covered; contract cannot override these acts |
| EU | CJEU *SAS Institute v WPL* (C-406/10): functionality and data formats unprotected; *Top System* (C-13/20) | Reimplementing the protocol copies no protected expression |
| EU | CJEU *Ryanair v PR Aviation* (C-30/14): where no IP right exists, contractual use restrictions remain possible | The "publicly supported interfaces" clause likely survives as a **service-use** term |
| Germany | UrhG §69d(3), §69e, §69g(2); GeschGehG §3(1) No. 2 (reverse engineering of available products); BGH I ZR 224/12 (ignoring a ToS ban on automated access is not by itself unfair; overcoming *technical* measures can be) | Copyright and trade-secret exposure low; don't defeat technical blocks |
| Germany | StGB §202a requires data not intended for you and overcoming special security | Using your own credentials for your own data does not fit |
| Russia | Civil Code art. 1280 §2–3 (study/testing; decompilation for interoperability, not overridable by contract), art. 1259(5) (ideas/methods unprotected); Criminal Code art. 272/273 | Low; criminal tail risk only on a rights-holder complaint, very unlikely for a user-authorised personal tool |
| US (GitHub nexus) | DMCA §1201(f); *Sega v Accolade*; *Van Buren*; *hiQ v LinkedIn* (lost on contract); *Facebook v Power Ventures* (access after explicit revocation violates CFAA) | Contract claims are the live risk; **continuing after a cease-and-desist or block is what turns a ToS issue into a legal one** |

## 4. Risk by usage scenario

### 4.1 Personal use on your own account (from a VPS)
1. Account suspension/termination — **low to moderate**, the only realistic sanction.
2. Data corruption — self-inflicted, has already happened in this project
   (`docs/crash-investigation.md`, KI-01, KI-25, KI-26); Cultured Code disclaims it.
3. Cease-and-desist — very low. Damages — negligible. Criminal — negligible.
4. Data protection — household exemption (GDPR Art. 2(2)(c); 152-FZ Art. 1(2)(1)). The
   real issue is **security**: your Things password and a full copy of your tasks sit on
   a VPS.

### 4.2 Publishing the open-source fork
1. Public criticism or cease-and-desist — low.
2. GitHub takedown — low; a §1201 claim looks weak and GitHub reviews them; a trademark
   complaint is plausible only with confusing branding.
3. Damages — low; free software is largely treated as a gift under German law (liability
   for intent/gross negligence). Knowingly shipping write paths that crashed clients,
   without guardrails, could be argued as gross negligence — another reason for
   ADR-0004.

### 4.3 Hosted service for others — avoid
Collecting other people's Things Cloud credentials, GDPR controller duties (and likely
an EU representative), Russian data-localisation rules for Russian users, unfair
competition law, possible EU Product Liability Directive 2024/2853 exposure for
commercial software (applies to products placed on the market after 9 Dec 2026;
non-commercial open source excluded), and *Power Ventures*-style revocation risk.

## 5. Mitigations adopted by this project

| Mitigation | Where |
|---|---|
| Self-host only, one owner per instance; no hosted offering | `roadmap.md` (out of scope), README |
| Prominent non-affiliation and risk disclaimer; "Things" used only to describe compatibility; no logos or screenshots | README |
| Mandatory authentication on every data route: OAuth tokens for the owner only, or no network exposure (local-only mode on `127.0.0.1`) | done in code (KI-07, ADR-0002) |
| Guarded writes: allowlist, preconditions, recurrence disabled, audit log | ADR-0004 |
| Read-only by default: write tools exist only with `ENABLE_WRITES=true` | done (phase 0, ADR-0004) |
| Remove account surgery (sign-up, accept SLA, delete account) from the server | KI-08 |
| Rate limiting and coalesced syncs; never hammer the cloud | KI-27 |
| No identity or IP rotation to evade blocks. **If blocked or contacted by Cultured Code: stop** | this document |
| Protocol docs written in our own words from observed traffic; no decompiled code, binaries or vendor text | `protocol/things-cloud.md` |
| Secrets never logged; VPS hardened; data at rest protected | KI-09, `deployment/vps.md` |
| Test on a separate Things account | AGENTS.md rule 1 |

### 5.1 Client identity (User-Agent) — open decision
The client presents itself as Things for Mac (`ThingsMac/32209501`, fixed fake device
metadata). Options:

| Option | Pro | Con |
|---|---|---|
| Keep impersonating | Known to work | Weakens "honest use" arguments; one fake fingerprint shared by every user of this code |
| Honest UA, e.g. `things-cloud-mcp/<ver> (+repo; unofficial)` | Transparent; lets Cultured Code rate-limit or contact | Might be rejected; makes blocking trivial |

Test the honest variant on the sandbox account before deciding (KI-30). Whatever is
chosen, do not rotate identities.

### 5.2 Suggested disclaimer text
> Unofficial community project. Not affiliated with, endorsed or supported by Cultured
> Code GmbH & Co. KG. "Things" is a trademark of its owner, used only to describe
> compatibility. Uses an undocumented protocol that may change without notice.
> Cultured Code advises against tools that access Things Cloud directly. Writing data
> can corrupt your Things database or crash Things apps. Use at your own risk, back up
> first, and test on a separate account. Provided "as is" under the MIT licence.

## 6. Officially supported alternatives
- A Mac-local MCP server using the URL scheme and AppleScript (e.g.
  [`hald/things-mcp`](https://github.com/hald/things-mcp)) — safe by Cultured Code's
  definition, but needs a Mac online for remote use.
- Mail to Things from a server — create-only (title + note), 100 emails/day.

## 7. Not verified
Trademark registrations for "Things"; private enforcement (letters, bans, server-side
blocks); whether Things Cloud requires the `ThingsMac` user agent; governing law for
non-EU users; how a German court would treat the "publicly supported interfaces"
clause under standard-terms review.
