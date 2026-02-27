#!/bin/bash
# =============================================================================
# Mnemonic User Test — Part 3: Data Quality Evaluation
# =============================================================================
# Queries the running daemon's API to evaluate whether the data it created
# is useful, coherent, and human-friendly. Produces a structured report.
#
# Usage:
#   chmod +x tests/usertest/03_evaluate.sh
#   ./tests/usertest/03_evaluate.sh
#
# Prerequisites:
#   - mnemonic daemon running with data from parts 1 and/or 2
# =============================================================================

set -e

API="http://127.0.0.1:9999/api/v1"

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
CYAN='\033[0;36m'
YELLOW='\033[0;33m'
GRAY='\033[0;90m'
BOLD='\033[1m'
NC='\033[0m'

pass_count=0
warn_count=0
fail_count=0

# Helpers
pass() { echo -e "  ${GREEN}PASS${NC} $1"; pass_count=$((pass_count + 1)); }
warn() { echo -e "  ${YELLOW}WARN${NC} $1"; warn_count=$((warn_count + 1)); }
fail() { echo -e "  ${RED}FAIL${NC} $1"; fail_count=$((fail_count + 1)); }
info() { echo -e "  ${GRAY}INFO${NC} $1"; }
header() { echo ""; echo -e "${BOLD}${CYAN}═══ $1 ═══${NC}"; }

# Check daemon is reachable
echo -e "${BOLD}Mnemonic Data Quality Evaluation${NC}"
echo ""
if ! curl -sf "$API/health" > /dev/null 2>&1; then
    echo -e "${RED}ERROR: Cannot reach daemon at $API. Is it running?${NC}"
    exit 1
fi
echo -e "${GREEN}Daemon is reachable.${NC}"

# =======================================================================
header "1. STORE HEALTH — Do we have data?"
# =======================================================================

STATS=$(curl -sf "$API/stats")
if [ -z "$STATS" ]; then
    fail "Could not fetch stats"
else
    TOTAL=$(echo "$STATS" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('store',d).get('total_memories',0))" 2>/dev/null || echo 0)
    ACTIVE=$(echo "$STATS" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('store',d).get('active_memories',0))" 2>/dev/null || echo 0)
    ASSOC=$(echo "$STATS" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('store',d).get('total_associations',0))" 2>/dev/null || echo 0)
    DB_SIZE=$(echo "$STATS" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('store',d).get('storage_size_bytes',0))" 2>/dev/null || echo 0)

    info "Total memories: $TOTAL"
    info "Active memories: $ACTIVE"
    info "Associations: $ASSOC"
    info "DB size: $((DB_SIZE / 1024)) KB"

    if [ "$TOTAL" -ge 10 ]; then
        pass "At least 10 memories stored ($TOTAL)"
    elif [ "$TOTAL" -ge 1 ]; then
        warn "Only $TOTAL memories — expected at least 10 from the injection script"
    else
        fail "No memories stored — encoding may not be working"
    fi

    if [ "$ASSOC" -ge 5 ]; then
        pass "Associations created ($ASSOC) — memories are being linked"
    elif [ "$ASSOC" -ge 1 ]; then
        warn "Only $ASSOC associations — expected more cross-linking"
    else
        fail "No associations — the encoding agent may not be finding similarities"
    fi
fi

# =======================================================================
header "2. MEMORY QUALITY — Are summaries readable?"
# =======================================================================

MEMORIES=$(curl -sf "$API/memories?limit=50")
if [ -z "$MEMORIES" ]; then
    fail "Could not fetch memories"
else
    MEM_COUNT=$(echo "$MEMORIES" | python3 -c "
import sys, json
data = json.load(sys.stdin)
mems = data if isinstance(data, list) else data.get('memories', [])
print(len(mems))
" 2>/dev/null || echo 0)

    # Check summary quality
    QUALITY=$(echo "$MEMORIES" | python3 -c "
import sys, json

data = json.load(sys.stdin)
mems = data if isinstance(data, list) else data.get('memories', [])

empty_summary = 0
short_summary = 0  # < 10 chars
long_summary = 0   # > 200 chars
good_summary = 0
has_concepts = 0
no_concepts = 0
garbled = 0

for m in mems:
    summary = m.get('summary', '')
    concepts = m.get('concepts', [])

    if not summary:
        empty_summary += 1
    elif len(summary) < 10:
        short_summary += 1
    elif len(summary) > 200:
        long_summary += 1
    else:
        good_summary += 1

    # Check for garbled output (common LLM failure mode)
    if summary and (summary.count('{') > 2 or summary.count('\"') > 6 or 'json' in summary.lower()[:20]):
        garbled += 1

    if concepts and len(concepts) > 0:
        has_concepts += 1
    else:
        no_concepts += 1

total = len(mems) or 1
print(f'{good_summary},{empty_summary},{short_summary},{long_summary},{garbled},{has_concepts},{no_concepts},{total}')
" 2>/dev/null || echo "0,0,0,0,0,0,0,1")

    IFS=',' read -r GOOD EMPTY SHORT LONG GARBLED HAS_CONCEPTS NO_CONCEPTS MEM_TOTAL <<< "$QUALITY"

    info "Summaries: $GOOD good, $EMPTY empty, $SHORT too-short, $LONG too-long, $GARBLED garbled"
    info "Concepts: $HAS_CONCEPTS with concepts, $NO_CONCEPTS without"

    # Evaluate
    GOOD_PCT=$((GOOD * 100 / MEM_TOTAL))
    if [ "$GOOD_PCT" -ge 80 ]; then
        pass "Summary quality: ${GOOD_PCT}% of summaries are well-formed"
    elif [ "$GOOD_PCT" -ge 50 ]; then
        warn "Summary quality: only ${GOOD_PCT}% of summaries are well-formed"
    else
        fail "Summary quality: only ${GOOD_PCT}% of summaries are well-formed — LLM output may be poor"
    fi

    if [ "$GARBLED" -eq 0 ]; then
        pass "No garbled summaries (no raw JSON leaking into summaries)"
    else
        fail "$GARBLED garbled summaries detected — LLM is returning raw JSON instead of text"
    fi

    CONCEPT_PCT=$((HAS_CONCEPTS * 100 / MEM_TOTAL))
    if [ "$CONCEPT_PCT" -ge 70 ]; then
        pass "Concept extraction: ${CONCEPT_PCT}% of memories have concepts"
    elif [ "$CONCEPT_PCT" -ge 40 ]; then
        warn "Concept extraction: only ${CONCEPT_PCT}% have concepts"
    else
        fail "Concept extraction: only ${CONCEPT_PCT}% have concepts — extraction may be broken"
    fi

    # Print a few sample summaries for human review
    echo ""
    echo -e "  ${BOLD}Sample summaries (first 5):${NC}"
    echo "$MEMORIES" | python3 -c "
import sys, json
data = json.load(sys.stdin)
mems = data if isinstance(data, list) else data.get('memories', [])
for i, m in enumerate(mems[:5]):
    summary = m.get('summary', '(empty)')[:100]
    concepts = ', '.join(m.get('concepts', [])[:4])
    salience = m.get('salience', 0)
    print(f'    {i+1}. [{salience:.2f}] {summary}')
    if concepts:
        print(f'       concepts: {concepts}')
" 2>/dev/null
fi

# =======================================================================
header "3. EPISODES — Are temporal groupings working?"
# =======================================================================

EPISODES=$(curl -sf "$API/episodes?limit=20")
if [ -z "$EPISODES" ]; then
    fail "Could not fetch episodes"
else
    EP_EVAL=$(echo "$EPISODES" | python3 -c "
import sys, json

data = json.load(sys.stdin)
eps = data if isinstance(data, list) else data.get('episodes', [])

total = len(eps)
closed = sum(1 for e in eps if e.get('state') == 'closed')
with_title = sum(1 for e in eps if e.get('title') and e.get('title') != 'Untitled session')
with_summary = sum(1 for e in eps if e.get('summary'))
with_narrative = sum(1 for e in eps if e.get('narrative'))
with_tone = sum(1 for e in eps if e.get('emotional_tone') and e.get('emotional_tone') != 'neutral')

print(f'{total},{closed},{with_title},{with_summary},{with_narrative},{with_tone}')
" 2>/dev/null || echo "0,0,0,0,0,0")

    IFS=',' read -r EP_TOTAL EP_CLOSED EP_TITLED EP_SUMMARIZED EP_NARRATED EP_TONED <<< "$EP_EVAL"

    info "Episodes: $EP_TOTAL total, $EP_CLOSED closed"
    info "With title: $EP_TITLED, with summary: $EP_SUMMARIZED, with narrative: $EP_NARRATED"

    if [ "$EP_TOTAL" -ge 2 ]; then
        pass "Multiple episodes created ($EP_TOTAL) — temporal grouping is working"
    elif [ "$EP_TOTAL" -ge 1 ]; then
        warn "Only $EP_TOTAL episode — may need more time for gaps to trigger closure"
    else
        fail "No episodes created — episoding agent may not be working"
    fi

    if [ "$EP_TITLED" -ge 1 ]; then
        pass "Episodes have LLM-synthesized titles ($EP_TITLED/$EP_TOTAL)"
    else
        warn "No episodes have titles — LLM synthesis may be failing"
    fi

    if [ "$EP_NARRATED" -ge 1 ]; then
        pass "Episodes have narratives ($EP_NARRATED/$EP_TOTAL)"
    else
        warn "No episode narratives — synthesis may be producing incomplete output"
    fi

    # Print episode details
    echo ""
    echo -e "  ${BOLD}Episodes:${NC}"
    echo "$EPISODES" | python3 -c "
import sys, json
data = json.load(sys.stdin)
eps = data if isinstance(data, list) else data.get('episodes', [])
for i, e in enumerate(eps[:6]):
    title = e.get('title', '(untitled)')[:60]
    state = e.get('state', '?')
    tone = e.get('emotional_tone', '-')
    outcome = e.get('outcome', '-')
    events = len(e.get('raw_memory_ids', []))
    dur = e.get('duration_sec', 0)
    print(f'    {i+1}. [{state}] {title}')
    print(f'       events={events}, duration={dur}s, tone={tone}, outcome={outcome}')
    summary = e.get('summary', '')
    if summary:
        print(f'       summary: {summary[:120]}')
" 2>/dev/null
fi

# =======================================================================
header "4. RETRIEVAL QUALITY — Do queries return relevant results?"
# =======================================================================

run_query() {
    local query="$1"
    local expect_keyword="$2"
    local description="$3"

    RESULT=$(curl -sf -X POST "$API/query" \
        -H "Content-Type: application/json" \
        -d "{\"query\": \"$query\", \"synthesize\": true}" 2>/dev/null)

    if [ -z "$RESULT" ]; then
        fail "Query '$description' — no response from API"
        return
    fi

    EVAL=$(echo "$RESULT" | python3 -c "
import sys, json

data = json.load(sys.stdin)
memories = data.get('memories', [])
synthesis = data.get('synthesis', '')
count = len(memories)

# Check if expected keyword appears in top 3 results
keyword = '$expect_keyword'.lower()
found_in_top3 = False
top_summaries = []
for m in memories[:3]:
    mem = m.get('memory', m)
    summary = mem.get('summary', '')
    content = mem.get('content', '')
    top_summaries.append(summary[:80])
    if keyword in summary.lower() or keyword in content.lower():
        found_in_top3 = True

# Check synthesis quality
synth_len = len(synthesis)
synth_has_keyword = keyword in synthesis.lower() if synthesis else False

print(f'{count}|{found_in_top3}|{synth_len}|{synth_has_keyword}')
for s in top_summaries[:3]:
    print(f'SUMMARY:{s}')
if synthesis:
    print(f'SYNTH:{synthesis[:150]}')
" 2>/dev/null)

    FIRST_LINE=$(echo "$EVAL" | head -1)
    IFS='|' read -r Q_COUNT Q_FOUND Q_SYNTH_LEN Q_SYNTH_HAS <<< "$FIRST_LINE"

    if [ "$Q_FOUND" = "True" ]; then
        pass "Query '$description' — found '$expect_keyword' in top 3 results ($Q_COUNT total)"
    elif [ "$Q_COUNT" -gt 0 ]; then
        warn "Query '$description' — got $Q_COUNT results but '$expect_keyword' not in top 3"
    else
        fail "Query '$description' — returned 0 results"
    fi

    # Show top results
    echo "$EVAL" | grep '^SUMMARY:' | head -3 | while read -r line; do
        echo -e "    ${GRAY}${line#SUMMARY:}${NC}"
    done
    SYNTH_LINE=$(echo "$EVAL" | grep '^SYNTH:' | head -1)
    if [ -n "$SYNTH_LINE" ]; then
        echo -e "    ${CYAN}synthesis: ${SYNTH_LINE#SYNTH:}${NC}"
    fi
    echo ""
}

echo -e "  Running 6 test queries...\n"

run_query "payment webhook errors" "webhook" "Relevant: webhook debugging"
run_query "which auth library for SSO" "saml" "Relevant: SSO research"
run_query "onboarding A/B test results" "onboarding" "Relevant: status update"
run_query "rate limiting and webhooks" "rate" "Cross-episode: PR + webhook"
run_query "what do I have tomorrow" "tomorrow" "Vague: tomorrow planning"
run_query "kubernetes cluster migration" "kubernetes" "Irrelevant: should find nothing"

# =======================================================================
header "5. GRAPH QUALITY — Are associations meaningful?"
# =======================================================================

GRAPH=$(curl -sf "$API/graph?limit=100")
if [ -z "$GRAPH" ]; then
    fail "Could not fetch graph data"
else
    GRAPH_EVAL=$(echo "$GRAPH" | python3 -c "
import sys, json

data = json.load(sys.stdin)
nodes = data.get('nodes', [])
edges = data.get('edges', [])

# Basic counts
node_count = len(nodes)
edge_count = len(edges)

# Check connectivity
connected_nodes = set()
for e in edges:
    connected_nodes.add(e.get('source', ''))
    connected_nodes.add(e.get('target', ''))
isolated = node_count - len(connected_nodes) if node_count > 0 else 0

# Relation type distribution
rel_types = {}
for e in edges:
    rt = e.get('relation_type', 'unknown')
    rel_types[rt] = rel_types.get(rt, 0) + 1

# Average strength
avg_strength = 0
if edges:
    avg_strength = sum(e.get('strength', 0) for e in edges) / len(edges)

print(f'{node_count}|{edge_count}|{isolated}|{avg_strength:.2f}')
for rt, count in sorted(rel_types.items(), key=lambda x: -x[1]):
    print(f'REL:{rt}={count}')
" 2>/dev/null)

    FIRST_LINE=$(echo "$GRAPH_EVAL" | head -1)
    IFS='|' read -r G_NODES G_EDGES G_ISOLATED G_AVG_STR <<< "$FIRST_LINE"

    info "Graph: $G_NODES nodes, $G_EDGES edges, $G_ISOLATED isolated nodes"
    info "Average edge strength: $G_AVG_STR"

    # Show relation type breakdown
    echo "$GRAPH_EVAL" | grep '^REL:' | while read -r line; do
        info "  ${line#REL:}"
    done

    if [ "$G_EDGES" -ge 5 ]; then
        pass "Graph has meaningful connectivity ($G_EDGES edges)"
    elif [ "$G_EDGES" -ge 1 ]; then
        warn "Graph has few edges ($G_EDGES) — associations may be weak"
    else
        fail "Graph has no edges — association creation may be broken"
    fi

    ISOLATED_PCT=0
    if [ "$G_NODES" -gt 0 ]; then
        ISOLATED_PCT=$((G_ISOLATED * 100 / G_NODES))
    fi
    if [ "$ISOLATED_PCT" -le 30 ]; then
        pass "Most nodes are connected (only ${ISOLATED_PCT}% isolated)"
    elif [ "$ISOLATED_PCT" -le 60 ]; then
        warn "${ISOLATED_PCT}% of nodes are isolated — expected more connections"
    else
        fail "${ISOLATED_PCT}% of nodes are isolated — graph is mostly disconnected"
    fi
fi

# =======================================================================
header "RESULTS SUMMARY"
# =======================================================================

TOTAL=$((pass_count + warn_count + fail_count))
echo ""
echo -e "  ${GREEN}PASS: $pass_count${NC}  ${YELLOW}WARN: $warn_count${NC}  ${RED}FAIL: $fail_count${NC}  (total: $TOTAL checks)"
echo ""

if [ "$fail_count" -eq 0 ] && [ "$warn_count" -eq 0 ]; then
    echo -e "  ${BOLD}${GREEN}EXCELLENT — All checks passed. The data looks great.${NC}"
elif [ "$fail_count" -eq 0 ]; then
    echo -e "  ${BOLD}${YELLOW}GOOD — No failures, but some areas could be better.${NC}"
elif [ "$fail_count" -le 2 ]; then
    echo -e "  ${BOLD}${YELLOW}MIXED — A few things aren't working right. Check the failures above.${NC}"
else
    echo -e "  ${BOLD}${RED}POOR — Multiple failures. The pipeline may have issues.${NC}"
fi
echo ""
