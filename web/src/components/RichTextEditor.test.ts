// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import RichTextEditor from './RichTextEditor.vue'

type EditorExposed = {
  getHTML: () => string
  selectAll: () => void
  setContent: (value: string) => void
  setTextSelection: (position: number) => void
  importMarkdownFile: (file: File) => Promise<void>
  insertMarkdown: (value: string, replace?: boolean) => boolean
}

let wrapper: VueWrapper | undefined
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  document.body.innerHTML = ''
})

function exposed() {
  return wrapper!.vm as unknown as EditorExposed
}

describe('RichTextEditor', () => {
  it('keeps the selection when applying italic from the toolbar', async () => {
    wrapper = mount(RichTextEditor, { props: { modelValue: '<p>测试文本</p>' }, attachTo: document.body })
    await nextTick()
    exposed().selectAll()
    await wrapper.get('button[aria-label="斜体"]').trigger('click')
    expect(exposed().getHTML()).toContain('<em>测试文本</em>')
  })

  it('parses pasted Markdown as rich content', async () => {
    wrapper = mount(RichTextEditor, { props: { modelValue: '' }, attachTo: document.body })
    await nextTick()
    exposed().insertMarkdown('## 标题\n\n**加粗** 和 *斜体*\n\n- 第一项\n- 第二项', true)
    await nextTick()
    expect(exposed().getHTML()).toContain('<h2>标题</h2>')
    expect(exposed().getHTML()).toContain('<strong>加粗</strong>')
    expect(exposed().getHTML()).toContain('<em>斜体</em>')
    expect(exposed().getHTML()).toContain('<ul>')
  })

  it('imports Markdown files and switches code-block language', async () => {
    wrapper = mount(RichTextEditor, { props: { modelValue: '' }, attachTo: document.body })
    await nextTick()
    await exposed().importMarkdownFile(new File(['```javascript\nconst answer = 42\n```'], 'memory.md', { type: 'text/markdown' }))
    exposed().setTextSelection(2)
    await nextTick()
    const language = wrapper.get('#code-language')
    await language.setValue('typescript')
    expect(exposed().getHTML()).toContain('class="language-typescript"')
    expect(exposed().getHTML()).toContain('const answer = 42')
  })
})
