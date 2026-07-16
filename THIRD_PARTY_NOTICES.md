# Third-party notices

## Agent Threat Rules

The experimental searches under `examples/detections/ai-agent/` adapt threat
concepts from Agent Threat Rules (ATR) at pinned revision
`0c7a1f133fc176a732767363db65102aa0aae710`:

- ATR-2026-00702, Credential / API Key Exfiltration via Agent Action
- ATR-2026-00711, System Sabotage via Destructive Shell Command
- ATR-2026-00714, Forced Specific Tool Invocation

Source project: https://github.com/Agent-Threat-Rule/agent-threat-rules

The querysplunk searches use original SPL, Splunk field mapping, safety bounds,
and result contracts. They do not include the ATR converter. The source rule
IDs, concepts, and selected pattern semantics are attributed under the
following license.

MIT License

Copyright (c) 2026 ATR Contributors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
