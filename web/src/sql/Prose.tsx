import type { ReactNode } from 'react'

// Prose shows a text tile's words in a small, safe subset of Markdown, built as elements (never
// as HTML, so no text can inject markup): # to ### headings, paragraphs (a blank line between
// them), lists ("- " or "1. "), **bold** and *italic*.
const item = /^\s*([-*]|\d+[.)])\s+/
const heading = /^(#{1,3})\s+(.*)$/

export default function Prose({ text }: { text: string }) {
  const lines = text.replace(/\r\n?/g, '\n').split('\n')
  const blocks: ReactNode[] = []
  for (let i = 0; i < lines.length;) {
    const h = heading.exec(lines[i])
    if (!lines[i].trim()) {
      i++
    } else if (h) {
      const H = (['h2', 'h3', 'h4'] as const)[h[1].length - 1]
      blocks.push(<H key={blocks.length}>{inline(h[2])}</H>)
      i++
    } else if (item.test(lines[i])) {
      const ordered = /^\s*\d/.test(lines[i])
      const items: ReactNode[] = []
      for (; i < lines.length && item.test(lines[i]); i++) items.push(<li key={i}>{inline(lines[i].replace(item, ''))}</li>)
      blocks.push(ordered ? <ol key={blocks.length}>{items}</ol> : <ul key={blocks.length}>{items}</ul>)
    } else {
      const para: string[] = []
      for (; i < lines.length && lines[i].trim() && !heading.test(lines[i]) && !item.test(lines[i]); i++) para.push(lines[i].trim())
      blocks.push(<p key={blocks.length}>{inline(para.join(' '))}</p>)
    }
  }
  return <div className="prose">{blocks}</div>
}

// inline: **bold** and *italic*; anything else as typed
function inline(s: string): ReactNode[] {
  return s.split(/(\*\*[^*]+\*\*|\*[^*\s][^*]*\*)/g).filter(Boolean).map((part, k) =>
    part.length > 4 && part.startsWith('**') && part.endsWith('**') ? <strong key={k}>{part.slice(2, -2)}</strong>
    : part.length > 2 && part.startsWith('*') && part.endsWith('*') ? <em key={k}>{part.slice(1, -1)}</em>
    : part)
}
