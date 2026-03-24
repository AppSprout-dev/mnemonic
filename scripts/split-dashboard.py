#!/usr/bin/env python3
"""Split the monolithic index.html into modular ES module files.

Reads internal/web/static/index.html and produces:
  - css/base.css           (non-theme CSS: reset, layout, scrollbars, view system)
  - css/nav.css            (forum-style nav, footer, breadcrumbs)
  - css/drawer.css         (activity drawer styles)
  - css/pages/recall.css   (recall view styles)
  - css/pages/timeline.css (timeline view styles)
  - css/pages/explore.css  (explore/forum view styles)
  - css/pages/sdk.css      (SDK/agent view styles)
  - css/pages/llm.css      (LLM view styles)
  - css/pages/tools.css    (tools view styles)
  - js/state.js            (shared state object)
  - js/api.js              (CONFIG, apiFetch, fetchJSON, WebSocket)
  - js/utils.js            (escapeHtml, relativeTime, formatBytes, markdown, etc.)
  - js/app.js              (init, router, theme, keyboard shortcuts, polling)
  - js/pages/recall.js     (recall view logic)
  - js/pages/timeline.js   (timeline view logic)
  - js/pages/explore.js    (explore view logic)
  - js/pages/sdk.js        (SDK/agent view logic)
  - js/pages/llm.js        (LLM view logic)
  - js/pages/tools.js      (tools view logic)
  - js/pages/mind.js       (mind/graph view logic - to be killed)

This is a one-time migration tool. After running, manually verify
each file and adjust imports/exports.

Usage: python3 scripts/split-dashboard.py
"""

import re
import os

SRC = "internal/web/static/index.html"
OUT = "internal/web/static"

def read_source():
    with open(SRC, "r") as f:
        return f.read()

def extract_between(text, start_marker, end_marker, inclusive=False):
    """Extract text between two markers."""
    start_idx = text.find(start_marker)
    if start_idx == -1:
        return ""
    if not inclusive:
        start_idx += len(start_marker)
    end_idx = text.find(end_marker, start_idx + (len(start_marker) if inclusive else 0))
    if end_idx == -1:
        return text[start_idx:]
    if inclusive:
        end_idx += len(end_marker)
    return text[start_idx:end_idx]

def extract_css_sections(css_text):
    """Split CSS into logical sections based on comment markers."""
    sections = {}

    # Map comment markers to output files
    section_map = {
        'Nav': 'nav',
        'Views': 'base',
        'Recall': 'recall',
        'Results': 'recall',
        'Feedback': 'recall',
        'Synthesis': 'recall',
        'Remember': 'recall',
        'Timeline': 'timeline',
        'Explore': 'explore',
        'Patterns': 'explore',
        'Abstractions': 'explore',
        'Episodes': 'explore',
        'Mind': 'mind',
        'Agent': 'sdk',
        'SDK': 'sdk',
        'LLM': 'llm',
        'Tools': 'tools',
        'Research': 'tools',
        'Activity': 'drawer',
        'Drawer': 'drawer',
        'Toast': 'base',
        'Responsive': 'base',
        'Scrollbar': 'base',
        'Tooltip': 'base',
    }

    # Find all section comments like /* ── Section Name ── */
    pattern = r'/\*\s*──\s*(.*?)\s*──\s*\*/'
    matches = list(re.finditer(pattern, css_text))

    for i, match in enumerate(matches):
        section_name = match.group(1).strip()
        start = match.start()
        end = matches[i + 1].start() if i + 1 < len(matches) else len(css_text)

        # Figure out which file this belongs to
        target = 'base'  # default
        for key, val in section_map.items():
            if key.lower() in section_name.lower():
                target = val
                break

        if target not in sections:
            sections[target] = []
        sections[target].append(css_text[start:end])

    return sections

def extract_js_functions(js_text):
    """Extract JS and categorize functions by their purpose."""
    # We'll do a simpler approach: output the entire JS as-is but
    # identify the major sections for manual splitting

    sections = {
        'state': [],
        'api': [],
        'utils': [],
        'app': [],
        'recall': [],
        'timeline': [],
        'explore': [],
        'mind': [],
        'sdk': [],
        'llm': [],
        'tools': [],
    }

    # Find all function declarations
    func_pattern = r'(?:async\s+)?function\s+(\w+)\s*\('
    funcs = list(re.finditer(func_pattern, js_text))

    # Categorize functions by name prefix/content
    categorization = {
        'state': ['var state', 'var CONFIG'],
        'api': ['apiFetch', 'fetchJSON', 'connectWebSocket', 'handleWsMessage'],
        'utils': ['escapeHtml', 'simpleMarkdown', 'renderMarkdown', 'relativeTime',
                  'formatBytes', 'makeDayBuckets', 'MONTHS'],
        'app': ['initializeApp', 'switchView', 'handleHash', 'setTheme', 'updateStats',
                'showToast', 'handleKeyboard'],
        'recall': ['performRecall', 'renderRecall', 'sendFeedback', 'renderRemember',
                   'submitRemember', 'toggleRemember'],
        'timeline': ['loadTimeline', 'renderTimeline', 'setupTimeline', 'highlightTimeline',
                     'applyTimeline', 'hoverTimeline', 'unhoverTimeline', 'toggleTimeline',
                     'handleTimelineScroll'],
        'explore': ['loadExplore', 'renderExplore', 'renderEpisodes', 'renderMemories',
                    'renderPatterns', 'renderAbstractions', 'archivePattern', 'dismissPattern',
                    'switchExploreTab'],
        'mind': ['loadMind', 'renderMind', 'buildMind', 'loadMindGraph', 'buildMindGraph',
                 'loadMindLanding'],
        'sdk': ['loadAgent', 'renderAgent', 'loadEvolution', 'sendAgentMessage',
                'renderAgentChat', 'loadConversation', 'renderSDK'],
        'llm': ['loadLLM', 'renderLLM', 'renderLLMChart', 'loadLLMUsage'],
        'tools': ['loadTools', 'renderTools', 'renderToolChart', 'loadToolUsage',
                  'loadAnalytics', 'renderAnalytics', 'renderLifecycle', 'renderSignal',
                  'renderRecallCurve', 'renderSparkline'],
    }

    # Write a report instead of trying to auto-split (too error-prone)
    report = []
    report.append("# JS Function Inventory")
    report.append(f"# Total functions found: {len(funcs)}")
    report.append("")

    for match in funcs:
        func_name = match.group(1)
        line_num = js_text[:match.start()].count('\n') + 1

        # Categorize
        category = 'unknown'
        for cat, prefixes in categorization.items():
            for prefix in prefixes:
                if func_name.lower().startswith(prefix.lower().replace('var ', '')):
                    category = cat
                    break
            if category != 'unknown':
                break

        # Try harder with broader matching
        if category == 'unknown':
            name_lower = func_name.lower()
            if 'recall' in name_lower or 'search' in name_lower or 'feedback' in name_lower or 'remember' in name_lower:
                category = 'recall'
            elif 'timeline' in name_lower or 'tl' in name_lower:
                category = 'timeline'
            elif 'explore' in name_lower or 'episode' in name_lower or 'pattern' in name_lower or 'abstraction' in name_lower or 'memor' in name_lower:
                category = 'explore'
            elif 'mind' in name_lower or 'graph' in name_lower or 'ego' in name_lower:
                category = 'mind'
            elif 'agent' in name_lower or 'sdk' in name_lower or 'evolution' in name_lower or 'chat' in name_lower:
                category = 'sdk'
            elif 'llm' in name_lower:
                category = 'llm'
            elif 'tool' in name_lower or 'analytics' in name_lower or 'lifecycle' in name_lower or 'signal' in name_lower or 'sparkline' in name_lower:
                category = 'tools'
            elif 'theme' in name_lower or 'init' in name_lower or 'switch' in name_lower or 'hash' in name_lower or 'keyboard' in name_lower or 'toast' in name_lower or 'stat' in name_lower:
                category = 'app'
            elif 'escape' in name_lower or 'markdown' in name_lower or 'format' in name_lower or 'relative' in name_lower or 'bucket' in name_lower:
                category = 'utils'
            elif 'fetch' in name_lower or 'api' in name_lower or 'ws' in name_lower or 'websocket' in name_lower:
                category = 'api'

        report.append(f"  {category:12s} | line {line_num:5d} | {func_name}")

    return report

def extract_html_body(html_text):
    """Extract the body HTML (between <body> and first <script>)."""
    body_start = html_text.find('<body>') + len('<body>')
    script_start = html_text.find('<script>', body_start)
    return html_text[body_start:script_start].strip()

def main():
    print("Reading source...")
    source = read_source()

    # Extract CSS (between <style> and </style>)
    style_start = source.find('<style>') + len('<style>')
    style_end = source.find('</style>')
    css_text = source[style_start:style_end]

    # Extract JS (between <script> after </style> and </script>)
    # Skip the D3 script tag (already removed)
    script_start = source.find('<script>', style_end) + len('<script>')
    script_end = source.rfind('</script>')
    js_text = source[script_start:script_end]

    # Extract HTML body
    html_body = extract_html_body(source)

    # === CSS SPLITTING ===
    print(f"\nCSS: {len(css_text)} characters")
    css_sections = extract_css_sections(css_text)

    # Also grab the reset/body/view system that comes before any section comments
    first_comment = css_text.find('/* ──')
    if first_comment > 0:
        preamble = css_text[:first_comment].strip()
        if 'base' not in css_sections:
            css_sections['base'] = []
        css_sections['base'].insert(0, preamble)

    print("\nCSS sections found:")
    for name, parts in sorted(css_sections.items()):
        total_lines = sum(p.count('\n') for p in parts)
        print(f"  {name:15s}: {total_lines:4d} lines ({len(parts)} sections)")

    # Write CSS files
    for name, parts in css_sections.items():
        content = '\n\n'.join(parts)
        if name in ('recall', 'timeline', 'explore', 'sdk', 'llm', 'tools', 'mind'):
            path = os.path.join(OUT, 'css', 'pages', f'{name}.css')
        else:
            path = os.path.join(OUT, 'css', f'{name}.css')

        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, 'w') as f:
            f.write(f'/* Auto-extracted from index.html — {name} */\n\n')
            f.write(content)
            f.write('\n')
        print(f"  Wrote: {path}")

    # === JS ANALYSIS ===
    print(f"\nJS: {len(js_text)} characters, {js_text.count(chr(10))} lines")
    report = extract_js_functions(js_text)

    report_path = os.path.join(OUT, 'js', 'FUNCTION_MAP.txt')
    os.makedirs(os.path.dirname(report_path), exist_ok=True)
    with open(report_path, 'w') as f:
        f.write('\n'.join(report))
        f.write('\n')
    print(f"\nJS function map written to: {report_path}")
    print('\n'.join(report[:30]))
    if len(report) > 30:
        print(f"  ... ({len(report) - 30} more)")

    # === HTML ===
    print(f"\nHTML body: {len(html_body)} characters, {html_body.count(chr(10))} lines")
    html_path = os.path.join(OUT, 'BODY_HTML.txt')
    with open(html_path, 'w') as f:
        f.write(html_body)
    print(f"  HTML body extracted to: {html_path}")

    # === SUMMARY ===
    print("\n" + "=" * 60)
    print("MIGRATION SUMMARY")
    print("=" * 60)
    print(f"  Source:       {SRC}")
    print(f"  CSS files:    {len(css_sections)}")
    print(f"  JS functions: {len([r for r in report if '|' in r])}")
    print(f"  HTML body:    {html_body.count(chr(10))} lines")
    print()
    print("Next steps:")
    print("  1. Review CSS files in css/ — merge/rename as needed")
    print("  2. Use FUNCTION_MAP.txt to manually split JS into modules")
    print("  3. Write new index.html shell referencing modular files")
    print("  4. Test: make build && systemctl --user restart mnemonic")

if __name__ == '__main__':
    main()
