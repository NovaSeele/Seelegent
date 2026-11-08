import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { GitFile } from '@/api/types'
import { flush, setupApp } from '@/test/app'

import { imageSides, isImagePath } from './git'
import ImageDiff from './ImageDiff.vue'
import ImagePreview from './ImagePreview.vue'

const file = (path: string, status: GitFile['status'], oldPath?: string): GitFile => ({
  path,
  status,
  additions: 0,
  deletions: 0,
  binary: true,
  ...(oldPath ? { oldPath } : {}),
})

const url = (path: string, version: string) =>
  `/api/v1/tasks/t1/git/image?path=${encodeURIComponent(path)}&version=${version}`

async function mountDiff(f: GitFile) {
  const { plugins } = await setupApp()
  return mount(ImageDiff, { props: { taskId: 't1', file: f }, global: { plugins } })
}

describe('ImageDiff', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('knows which files are images and which sides they show', () => {
    expect(isImagePath('a/Logo.PNG')).toBe(true)
    expect(isImagePath('icon.svg')).toBe(true)
    expect(isImagePath('notes.txt')).toBe(false)
    expect(imageSides(file('notes.txt', 'modified'))).toEqual([])
    expect(imageSides(file('b.png', 'renamed', 'a.png')).map((s) => [s.side, s.path])).toEqual([
      ['before', 'a.png'],
      ['after', 'b.png'],
    ])
  })

  it('shows before and after for a changed image', async () => {
    const wrapper = await mountDiff(file('img/logo.png', 'modified'))
    const imgs = wrapper.findAll('img')
    expect(imgs.map((i) => i.attributes('src'))).toEqual([
      url('img/logo.png', 'head'),
      url('img/logo.png', 'worktree'),
    ])
    expect(wrapper.text()).toContain('Before (HEAD)')
    expect(wrapper.text()).toContain('After (working tree)')
    expect(imgs[0]?.attributes('alt')).toBe('img/logo.png, Before (HEAD)')
  })

  it('shows after only for a new image and before only for a deleted one', async () => {
    for (const status of ['added', 'untracked'] as const) {
      const wrapper = await mountDiff(file('new.webp', status))
      expect(wrapper.findAll('img').map((i) => i.attributes('src'))).toEqual([
        url('new.webp', 'worktree'),
      ])
    }
    const gone = await mountDiff(file('old.gif', 'deleted'))
    expect(gone.findAll('img').map((i) => i.attributes('src'))).toEqual([url('old.gif', 'head')])
    expect(gone.text()).not.toContain('After')
  })
})

describe('ImagePreview', () => {
  afterEach(() => vi.unstubAllGlobals())

  async function mountPreview() {
    const { plugins } = await setupApp()
    return mount(ImagePreview, {
      props: { src: url('big.png', 'worktree'), label: 'After', alt: 'big.png, After' },
      global: { plugins },
    })
  }

  it('shows the pixel size once the image loads', async () => {
    const wrapper = await mountPreview()
    const img = wrapper.get('img')
    Object.defineProperty(img.element, 'naturalWidth', { value: 640 })
    Object.defineProperty(img.element, 'naturalHeight', { value: 480 })
    await img.trigger('load')
    expect(wrapper.text()).toContain('640 × 480 px')
  })

  it('says why an image could not be shown, from the server', async () => {
    const answers = [
      {
        status: 413,
        error: { code: 'too_large', message: 'm', details: { size: 12 * 1024 * 1024 } },
      },
      {
        status: 415,
        error: { code: 'unsupported_media_type', message: 'm', details: { reason: 'lfs_pointer' } },
      },
      { status: 415, error: { code: 'unsupported_media_type', message: 'm' } },
    ]
    const want = ['Too large to preview (12 MB).', 'Stored with Git LFS', 'cannot be previewed']
    for (const [i, answer] of answers.entries()) {
      vi.stubGlobal(
        'fetch',
        vi.fn(() =>
          Promise.resolve(
            new Response(JSON.stringify({ error: answer.error }), { status: answer.status }),
          ),
        ),
      )
      const wrapper = await mountPreview()
      await wrapper.get('img').trigger('error')
      expect(wrapper.find('img').exists()).toBe(false)
      await vi.waitFor(() => expect(wrapper.get('[role="status"]').text()).toContain(want[i]))
    }
  })

  it('keeps a plain message when the image loads but cannot be drawn', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(new Response('png', { status: 200 }))),
    )
    const wrapper = await mountPreview()
    await wrapper.get('img').trigger('error')
    await flush(4)
    expect(wrapper.get('[role="status"]').text()).toBe('The image could not be shown.')
  })
})
