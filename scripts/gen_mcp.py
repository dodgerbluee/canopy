from pathlib import Path
import json
root=Path(__file__).resolve().parents[1]
source=root/'contracts/mcp/v1/tools.json'
rows=json.loads(source.read_text());assert len({r['name'] for r in rows})==len(rows)
for r in rows:assert r['mode'] in ['read','direct','review'] and ':' in r['scope']
(root/'server/internal/agentapi/catalog/tools.json').write_bytes(source.read_bytes())
print(f'MCP: {len(rows)} finite named tools; authoritative manifest copied to embedded catalog.')
