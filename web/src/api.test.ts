// @vitest-environment happy-dom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, waitForMediaProcessing, type MediaProcessingJob } from './api'
import type { Media, MediaUsage } from './types'

const media: Media = { id: 'media', original_filename: 'test.mp4', media_type: 'video', mime_type: 'video/mp4', size_bytes: 1234, status: 'ready', has_preview: true }
const usage: MediaUsage = { used_bytes: 1234, reserved_bytes: 0, quota_bytes: 10000 }
function job(phase: MediaProcessingJob['phase']): MediaProcessingJob {
  return { id: 'job', media_id: 'media', phase, step: '', encoder: '', error_code: '', ...(phase === 'completed' ? { media } : {}) }
}
function result(phase: MediaProcessingJob['phase'], includeUsage = false) {
  return new Response(JSON.stringify({ job: job(phase), ...(includeUsage ? { usage } : {}) }), { status: 200, headers: { 'Content-Type': 'application/json' } })
}
const fetchMock = vi.fn()

describe('media processing polling', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })
  afterEach(() => {
    vi.clearAllTimers()
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  it('backs off from 1.5 to 5 seconds, omits repeated usage calculations, then obtains final media and usage', async () => {
    const times: number[] = []
    fetchMock.mockImplementation(() => {
      times.push(Date.now())
      return Promise.resolve(result(times.length < 7 ? 'transcoding' : 'completed', times.length === 8))
    })
    const onPhase = vi.fn()
    const promise = waitForMediaProcessing('job/id', onPhase)
    await vi.runAllTimersAsync()
    await expect(promise).resolves.toEqual({ media, usage })
    expect(times.slice(1).map((time, index) => time - times[index]!)).toEqual([1500, 2250, 3375, 5000, 5000, 5000, 0])
    expect(fetchMock.mock.calls.slice(0, 7).every(([url]) => url === '/api/media-upload-jobs/job%2Fid?include_usage=0')).toBe(true)
    expect(fetchMock.mock.calls[7]![0]).toBe('/api/media-upload-jobs/job%2Fid')
    expect(onPhase).toHaveBeenCalledWith(expect.objectContaining({ phase: 'completed', media }))
  })

  it('accepts usage returned by older servers without making an extra request', async () => {
    fetchMock.mockResolvedValue(result('completed', true))
    await expect(waitForMediaProcessing('job', vi.fn())).resolves.toEqual({ media, usage })
    expect(fetchMock).toHaveBeenCalledOnce()
  })

  it('rejects a completed job with missing usage instead of silently returning undefined', async () => {
    fetchMock.mockImplementation(() => Promise.resolve(result('completed')))
    await expect(waitForMediaProcessing('job', vi.fn())).rejects.toThrow('无法获取存储用量')
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('recovers from a transient failure fetching the final usage', async () => {
    fetchMock.mockResolvedValueOnce(result('completed'))
      .mockRejectedValueOnce(new TypeError('network'))
      .mockResolvedValueOnce(result('completed', true))
    const promise = waitForMediaProcessing('job', vi.fn())
    await vi.runAllTimersAsync()
    await expect(promise).resolves.toEqual({ media, usage })
    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual(['/api/media-upload-jobs/job?include_usage=0', '/api/media-upload-jobs/job', '/api/media-upload-jobs/job'])
  })

  it.each([401, 403, 404])('stops immediately on permanent HTTP %i errors', async (status) => {
    fetchMock.mockResolvedValue(new Response('{}', { status }))
    await expect(waitForMediaProcessing('job', vi.fn())).rejects.toMatchObject({ status })
    expect(fetchMock).toHaveBeenCalledOnce()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('limits consecutive network failures instead of polling forever', async () => {
    fetchMock.mockRejectedValue(new TypeError('offline'))
    const promise = waitForMediaProcessing('job', vi.fn()).catch(error => error)
    await vi.runAllTimersAsync()
    expect(await promise).toEqual(new TypeError('offline'))
    expect(fetchMock).toHaveBeenCalledTimes(20)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('stops polling after the overall processing deadline', async () => {
    fetchMock.mockImplementation(() => Promise.resolve(result('transcoding')))
    const promise = waitForMediaProcessing('job', vi.fn()).catch(error => error)
    await vi.advanceTimersByTimeAsync(131 * 60 * 1000)
    expect(await promise).toBeInstanceOf(ApiError)
    expect(await promise).toMatchObject({ status: 408 })
    expect(vi.getTimerCount()).toBe(0)
  })
})
