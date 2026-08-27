<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { EditorContent, useEditor } from '@tiptap/vue-3'
import StarterKit from '@tiptap/starter-kit'
import CodeBlockLowlight from '@tiptap/extension-code-block-lowlight'
import Link from '@tiptap/extension-link'
import Image from '@tiptap/extension-image'
import Table from '@tiptap/extension-table'
import TableRow from '@tiptap/extension-table-row'
import TableHeader from '@tiptap/extension-table-header'
import TableCell from '@tiptap/extension-table-cell'
import Placeholder from '@tiptap/extension-placeholder'
import MarkdownIt from 'markdown-it'
import { Bold, Braces, FileUp, Heading2, ImagePlus, Italic, Link2, List, ListOrdered, Pilcrow, Quote, Redo2, Strikethrough, Table2, Undo2 } from 'lucide-vue-next'
import { lowlight } from '../syntax'

const props = defineProps<{ modelValue: string; disabled?: boolean }>()
const emit = defineEmits<{
  'update:modelValue': [value: string]
  'update:text': [value: string]
  'request-image': []
}>()

const markdownInput = ref<HTMLInputElement | null>(null)
const importMessage = ref('')
const markdown = new MarkdownIt({ html: false, linkify: true, typographer: false })
const codeLanguages = [
  { value: '', label: '纯文本' },
  { value: 'js', label: 'JavaScript (js)' },
  { value: 'javascript', label: 'JavaScript' },
  { value: 'ts', label: 'TypeScript (ts)' },
  { value: 'typescript', label: 'TypeScript' },
  { value: 'html', label: 'HTML' },
  { value: 'css', label: 'CSS' },
  { value: 'json', label: 'JSON' },
  { value: 'bash', label: 'Shell / Bash' },
  { value: 'python', label: 'Python' },
  { value: 'go', label: 'Go' },
  { value: 'sql', label: 'SQL' },
  { value: 'java', label: 'Java' },
  { value: 'cpp', label: 'C / C++' },
  { value: 'rust', label: 'Rust' },
  { value: 'markdown', label: 'Markdown' },
]

function looksLikeMarkdown(value: string) {
  return /(^|\n)\s{0,3}(#{1,6}\s|>\s|(?:[-+*]|\d+[.)])\s|```|~~~)/m.test(value)
    || /\*\*[^*\n]+\*\*|__[^_\n]+__|~~[^~\n]+~~|\[[^\]]+\]\([^)]+\)/.test(value)
    || /(^|[\s(])[*_][^*_\n]+[*_](?=$|[\s).,!，。])/.test(value)
    || /(^|\n)\|.+\|\s*\n\s*\|?\s*:?-{3,}/m.test(value)
}

function insertMarkdown(value: string, replace = false) {
  if (!editor.value) return false
  const rendered = markdown.render(value)
  return replace ? editor.value.commands.setContent(rendered, true) : editor.value.commands.insertContent(rendered)
}

const editor = useEditor({
  content: props.modelValue,
  editable: !props.disabled,
  extensions: [
    StarterKit.configure({ heading: { levels: [2, 3] }, codeBlock: false }),
    CodeBlockLowlight.configure({ lowlight, defaultLanguage: null }),
    Link.configure({ openOnClick: false, autolink: true, linkOnPaste: true, HTMLAttributes: { rel: 'noopener noreferrer nofollow', target: '_blank' } }),
    Image.configure({ allowBase64: false, HTMLAttributes: { loading: 'lazy' } }),
    Table.configure({ resizable: true }),
    TableRow,
    TableHeader,
    TableCell,
    Placeholder.configure({ placeholder: '那天发生了什么？输入 “## + 空格” 可快速创建标题。' }),
  ],
  editorProps: {
    attributes: { class: 'rich-editor-content', 'aria-label': '回忆正文富文本编辑器' },
    handlePaste: (_view, event) => {
      const text = event.clipboardData?.getData('text/plain') ?? ''
      if (!text || !looksLikeMarkdown(text)) return false
      event.preventDefault()
      return insertMarkdown(text)
    },
    handleDrop: (_view, event) => {
      const file = event.dataTransfer?.files?.[0]
      if (!file || !file.name.toLowerCase().endsWith('.md')) return false
      event.preventDefault()
      void importMarkdownFile(file)
      return true
    },
  },
  onUpdate: ({ editor: current }) => {
    emit('update:modelValue', current.getHTML())
    emit('update:text', current.getText({ blockSeparator: '\n' }))
  },
})

watch(() => props.modelValue, (value) => {
  if (!editor.value || editor.value.getHTML() === value) return
  editor.value.commands.setContent(value, false)
})

watch(() => props.disabled, (disabled) => editor.value?.setEditable(!disabled))

function setLink() {
  if (!editor.value) return
  const previous = editor.value.getAttributes('link').href as string | undefined
  const href = window.prompt('输入链接地址（留空可移除链接）', previous ?? 'https://')
  if (href === null) return
  if (!href.trim()) editor.value.chain().focus().extendMarkRange('link').unsetLink().run()
  else editor.value.chain().focus().extendMarkRange('link').setLink({ href: href.trim() }).run()
}

function insertImage(src: string, alt = '') {
  editor.value?.chain().focus().setImage({ src, alt }).run()
}

async function importMarkdownFile(file: File) {
  importMessage.value = ''
  if (!file.name.toLowerCase().endsWith('.md') && file.type !== 'text/markdown') {
    importMessage.value = '请选择 .md 格式的 Markdown 文件。'
    return
  }
  if (file.size > 100 * 1024) {
    importMessage.value = 'Markdown 文件不能超过 100 KiB。'
    return
  }
  try {
    const content = await file.text()
    insertMarkdown(content, editor.value?.isEmpty ?? true)
    importMessage.value = `已导入 ${file.name}`
  } catch {
    importMessage.value = 'Markdown 文件读取失败。'
  }
}

function handleMarkdownInput(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (file) void importMarkdownFile(file)
}

function setCodeLanguage(event: Event) {
  const language = (event.target as HTMLSelectElement).value
  editor.value?.chain().focus().updateAttributes('codeBlock', { language: language || null }).run()
}

function handleEditorKeydown(event: KeyboardEvent) {
  if ((event.ctrlKey || event.metaKey) && event.shiftKey && event.key.toLowerCase() === 'm') {
    event.preventDefault()
    markdownInput.value?.click()
  }
}

defineExpose({
  insertImage,
  focus: () => editor.value?.commands.focus(),
  getHTML: () => editor.value?.getHTML() ?? '',
  selectAll: () => editor.value?.commands.selectAll(),
  setContent: (value: string) => editor.value?.commands.setContent(value, true),
  setTextSelection: (position: number) => editor.value?.commands.setTextSelection(position),
  importMarkdownFile,
  insertMarkdown,
})
onBeforeUnmount(() => editor.value?.destroy())
</script>

<template>
  <div class="rich-editor" :class="{ disabled }" @keydown="handleEditorKeydown">
    <div v-if="editor" class="rich-editor-toolbar" role="toolbar" aria-label="正文格式工具栏">
      <button type="button" :class="{ active: editor.isActive('paragraph') }" aria-label="正文" title="正文" @mousedown.prevent @click="editor.chain().focus().setParagraph().run()"><Pilcrow :size="18" /></button>
      <button type="button" :class="{ active: editor.isActive('heading', { level: 2 }) }" aria-label="标题" title="标题" @mousedown.prevent @click="editor.chain().focus().toggleHeading({ level: 2 }).run()"><Heading2 :size="18" /></button>
      <span class="toolbar-divider" aria-hidden="true"></span>
      <button type="button" :class="{ active: editor.isActive('bold') }" aria-label="加粗" title="加粗 Ctrl+B" @mousedown.prevent @click="editor.chain().focus().toggleBold().run()"><Bold :size="18" /></button>
      <button type="button" :class="{ active: editor.isActive('italic') }" aria-label="斜体" title="斜体 Ctrl+I" @mousedown.prevent @click="editor.chain().focus().toggleItalic().run()"><Italic :size="18" /></button>
      <button type="button" :class="{ active: editor.isActive('strike') }" aria-label="删除线" title="删除线" @mousedown.prevent @click="editor.chain().focus().toggleStrike().run()"><Strikethrough :size="18" /></button>
      <button type="button" :class="{ active: editor.isActive('blockquote') }" aria-label="引用" title="引用" @mousedown.prevent @click="editor.chain().focus().toggleBlockquote().run()"><Quote :size="18" /></button>
      <span class="toolbar-divider" aria-hidden="true"></span>
      <button type="button" :class="{ active: editor.isActive('bulletList') }" aria-label="无序列表" title="无序列表" @mousedown.prevent @click="editor.chain().focus().toggleBulletList().run()"><List :size="18" /></button>
      <button type="button" :class="{ active: editor.isActive('orderedList') }" aria-label="有序列表" title="有序列表" @mousedown.prevent @click="editor.chain().focus().toggleOrderedList().run()"><ListOrdered :size="18" /></button>
      <button type="button" :class="{ active: editor.isActive('link') }" aria-label="链接" title="添加链接" @mousedown.prevent @click="setLink"><Link2 :size="18" /></button>
      <button type="button" aria-label="插入图片" title="插入图片" @click="emit('request-image')"><ImagePlus :size="18" /></button>
      <button type="button" aria-label="导入 Markdown" title="导入 .md 文件 Ctrl+Shift+M" @click="markdownInput?.click()"><FileUp :size="18" /></button>
      <input ref="markdownInput" class="visually-hidden" type="file" accept=".md,text/markdown,text/plain" @change="handleMarkdownInput" />
      <button type="button" :class="{ active: editor.isActive('table') }" aria-label="插入表格" title="插入 3 × 3 表格" @mousedown.prevent @click="editor.chain().focus().insertTable({ rows: 3, cols: 3, withHeaderRow: true }).run()"><Table2 :size="18" /></button>
      <button type="button" :class="{ active: editor.isActive('codeBlock') }" aria-label="切换代码块" title="切换代码块" @mousedown.prevent @click="editor.chain().focus().toggleCodeBlock().run()"><Braces :size="18" /></button>
      <span class="toolbar-spacer"></span>
      <button type="button" :disabled="!editor.can().undo()" aria-label="撤销" title="撤销 Ctrl+Z" @mousedown.prevent @click="editor.chain().focus().undo().run()"><Undo2 :size="18" /></button>
      <button type="button" :disabled="!editor.can().redo()" aria-label="重做" title="重做 Ctrl+Shift+Z" @mousedown.prevent @click="editor.chain().focus().redo().run()"><Redo2 :size="18" /></button>
    </div>
    <div v-if="editor?.isActive('codeBlock')" class="code-block-controls">
      <label for="code-language">代码语言</label>
      <select id="code-language" :value="editor.getAttributes('codeBlock').language || ''" @change="setCodeLanguage"><option v-for="language in codeLanguages" :key="language.value" :value="language.value">{{ language.label }}</option></select>
      <button type="button" @click="editor.chain().focus().setParagraph().run()"><Pilcrow :size="16" />退出代码块</button>
    </div>
    <EditorContent :editor="editor" />
    <div class="rich-editor-hint"><span>{{ importMessage || '支持 Markdown 粘贴、拖入与文件导入' }}</span><span>标题、列表、引用和代码块也可输入语法后按空格</span></div>
  </div>
</template>
