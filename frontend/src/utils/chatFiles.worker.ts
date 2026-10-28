import { parseChatFile } from './chatFileParser'

self.onmessage = (event: MessageEvent<{ name: string; buffer: ArrayBuffer }>) => {
  try { self.postMessage({ attachment: parseChatFile(event.data.name, event.data.buffer) }) }
  catch (error) {
    const codes = ['size', 'unsupported', 'content', 'empty', 'sheets', 'rows', 'columns', 'cells', 'formula']
    self.postMessage({ error: error instanceof Error && codes.includes(error.message) ? error.message : 'parse' })
  }
}
