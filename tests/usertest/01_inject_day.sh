#!/bin/bash
# =============================================================================
# Mnemonic User Test — Part 1: Simulated Mixed Work Day
# =============================================================================
# This script injects a realistic sequence of events via `mnemonic remember`
# to simulate a full mixed work day. Events are spaced with delays to let
# the encoding/episoding pipeline process them naturally.
#
# The day has 4 distinct episodes with gaps between them:
#   Episode A: Morning — debugging a payment webhook bug
#   Episode B: Midday  — researching auth libraries for a new feature
#   Episode C: Afternoon — writing a project status update
#   Episode D: Late afternoon — reviewing a PR and planning tomorrow
#
# Usage:
#   chmod +x tests/usertest/01_inject_day.sh
#   ./tests/usertest/01_inject_day.sh
#
# Prerequisites:
#   - mnemonic daemon running (`mnemonic start`)
#   - LLM endpoint available (LM Studio)
# =============================================================================

set -e

MNEMONIC="mnemonic"
DELAY=20  # seconds between events (give LLM time to encode each one)
GAP=45    # seconds between episodes (>30s triggers idle close for testing)

# Colors
GREEN='\033[0;32m'
CYAN='\033[0;36m'
YELLOW='\033[0;33m'
GRAY='\033[0;90m'
BOLD='\033[1m'
NC='\033[0m'

event_num=0

remember() {
    event_num=$((event_num + 1))
    echo -e "${GRAY}[$event_num]${NC} ${CYAN}remembering:${NC} ${1:0:80}..."
    $MNEMONIC remember "$1"
    sleep $DELAY
}

episode_gap() {
    echo ""
    echo -e "${YELLOW}--- episode gap ($GAP seconds) ---${NC}"
    echo ""
    sleep $GAP
}

echo -e "${BOLD}${GREEN}Mnemonic User Test — Injecting Simulated Work Day${NC}"
echo -e "Each event is sent via 'mnemonic remember' with ${DELAY}s spacing."
echo -e "Episode gaps are ${GAP}s to trigger episode closure."
echo ""

# =========================================================================
# EPISODE A: Morning — Debugging a payment webhook bug
# =========================================================================
echo -e "${BOLD}Episode A: Debugging payment webhook bug${NC}"

remember "Got a Slack alert from payments team: webhook endpoint /api/webhooks/stripe returning 500 errors intermittently since 2am. About 12% of incoming webhooks are failing. Need to investigate urgently."

remember "Checked the error logs with: grep 'webhook' /var/log/app/error.log | tail -50. Seeing 'context deadline exceeded' errors from the database connection pool. The webhook handler tries to write the event and verify idempotency in a single transaction."

remember "Found the root cause: the idempotency check does a SELECT with a full table scan on webhook_events because the idempotency_key column is missing an index. Under load, this query takes 3-8 seconds and the 5s context timeout kills it."

remember "Applied the fix: ALTER TABLE webhook_events ADD INDEX idx_idempotency_key (idempotency_key). Query time dropped from 3-8s to <1ms. Verified with EXPLAIN ANALYZE."

remember "Deployed the fix to staging, ran the webhook load test suite: 500 concurrent webhooks, zero failures. Pushed to production. Monitoring shows 0% error rate for the last 15 minutes. Closing the incident."

remember "Wrote a post-mortem note: the webhook_events table grew to 2.3M rows over 6 months but nobody added the idempotency index when the feature was built. Added a TODO to audit all tables for missing indexes."

episode_gap

# =========================================================================
# EPISODE B: Midday — Researching auth libraries for SSO feature
# =========================================================================
echo -e "${BOLD}Episode B: Researching auth libraries for SSO feature${NC}"

remember "Starting research on SSO integration for the admin dashboard. The product team wants SAML and OIDC support for enterprise customers. Need to evaluate Go libraries."

remember "Evaluated crewjam/saml — mature SAML 2.0 library for Go. Supports SP-initiated and IdP-initiated flows. Last commit 3 months ago, 800+ stars. Good docs but SAML-only, no OIDC."

remember "Evaluated coreos/go-oidc — solid OIDC library from CoreOS. Used by Dex and many projects. Clean API, good token verification. But OIDC only, no SAML support."

remember "Evaluated casdoor — full-featured auth platform written in Go. Supports SAML, OIDC, OAuth2, LDAP. But it's a whole separate service to deploy and manage, feels like overkill for our use case."

remember "Decision: going with crewjam/saml for SAML + coreos/go-oidc for OIDC as separate providers behind a common auth interface. This keeps dependencies focused and avoids deploying a whole auth platform. Will write up an RFC this week."

remember "Sketched out the auth interface: type SSOProvider interface { InitiateLogin(w, r) error; HandleCallback(w, r) (User, error); Metadata() ([]byte, error) }. Both SAML and OIDC adapters can implement this cleanly."

episode_gap

# =========================================================================
# EPISODE C: Afternoon — Writing project status update
# =========================================================================
echo -e "${BOLD}Episode C: Writing project status update${NC}"

remember "Writing the weekly status update for the eng team. This week: payment webhook incident, SSO research started, and the new onboarding flow shipped to 10% of users."

remember "Onboarding flow A/B test results after 5 days: new flow has 34% higher completion rate (72% vs 54%), average time to first action dropped from 8.2 minutes to 3.1 minutes. Statistical significance p<0.01."

remember "Risk item for status update: the search indexer has been falling behind by about 4 hours during peak traffic. ElasticSearch cluster needs a capacity upgrade or we need to batch index updates more aggressively."

remember "Finished the status update and posted it to the #engineering channel. Also tagged the infra team about the ElasticSearch capacity issue so they can plan for next sprint."

episode_gap

# =========================================================================
# EPISODE D: Late afternoon — PR review and planning
# =========================================================================
echo -e "${BOLD}Episode D: PR review and tomorrow planning${NC}"

remember "Reviewing PR #847 from Sarah: adds rate limiting to the public API. She used a token bucket algorithm with Redis as the backing store. Clean implementation, good tests covering edge cases like clock skew."

remember "Left feedback on PR #847: suggested extracting the rate limiter into its own package since we'll need it for the webhook endpoint too. Also noticed the Redis key TTL is set to 60s but the window is 300s — that's a bug, keys will expire mid-window."

remember "Sarah acknowledged the TTL bug and pushed a fix. Approved the PR after verifying the updated tests pass. Good work overall — clean code, solid tests."

remember "Planning tomorrow: morning standup at 9:30, then I need to start writing the SSO RFC document. Afternoon I have a 1:1 with the product manager about Q2 priorities. Also need to follow up on the ElasticSearch capacity issue."

remember "Personal note: dentist appointment Thursday at 2pm, need to block that on my calendar. Also want to try that new ramen place for lunch tomorrow."

echo ""
echo -e "${BOLD}${GREEN}Injection complete!${NC} Sent $event_num events across 4 episodes."
echo ""
echo "Next steps:"
echo "  1. Wait ~2 minutes for encoding and episoding to finish"
echo "  2. Do some manual activity (see 02_manual_guide.md)"
echo "  3. Run the evaluation: ./tests/usertest/03_evaluate.sh"
