import { mount, flushPromises } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import PelicanTestModal from '../PelicanTestModal.vue'
import { CANDY_PROMPT } from '@/utils/intelligenceTest'

vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api/admin/accounts',()=>({getAvailableModels:vi.fn(async()=>[{id:'gpt-6-astra'}])}))
vi.mock('../PelicanScheduledTestsPanel.vue',()=>({default:{template:'<div />'}}))
vi.mock('../PelicanRecordsDashboard.vue',()=>({default:{template:'<div />'}}))
vi.mock('../PelicanAssessment.vue', () => ({ default: { props: ['output'], template: '<div data-testid="assessment-control" :data-output="output" />' } }))

describe('PelicanTestModal',()=>{
 afterEach(()=>{vi.unstubAllGlobals();localStorage.clear()})
 it('waits for automatic evaluation before displaying and saving the annotated artwork', async () => {
  const source = '<html><body><svg></svg></body></html>'
  const badge = '<!--sub2api:pelican-assessment:start--><aside data-sub2api-quality="normal">正常</aside><!--sub2api:pelican-assessment:end-->'
  const evaluated = source.replace('<body>', '<body>' + badge)
  let stream!: ReadableStreamDefaultController<Uint8Array>
  const encoder = new TextEncoder()
  const send = (event: unknown) => stream.enqueue(encoder.encode('data: ' + JSON.stringify(event) + '\n\n'))
  vi.stubGlobal('fetch', vi.fn(async () => new Response(new ReadableStream({ start(controller) { stream = controller } }))))
  const wrapper = mount(PelicanTestModal, { props: { show: true, account: { id: 41, name: 'Test', platform: 'openai', type: 'oauth' } as never }, global: { stubs: { BaseDialog: { template: '<div><slot/><slot name="footer"/></div>' }, Icon: true } } })
  await flushPromises()
  await wrapper.findAll('button').find(b => b.classes().includes('btn-primary'))!.trigger('click')
  await flushPromises()
  send({ type: 'content', text: source })
  send({ type: 'test_complete', success: true })
  send({ type: 'pelican_assessing' })
  await flushPromises()
  expect(wrapper.find('iframe').exists()).toBe(false)
  expect(wrapper.text()).toContain('pelicanTests.assessment.running')
  send({ type: 'pelican_assessment', text: evaluated })
  stream.close()
  await flushPromises()
  expect(wrapper.get('[data-testid="pelican-quality-badge"]').text()).toContain('pelicanTests.assessment.normal')
  expect(localStorage.getItem('sub2api-pelican-test:41')).toContain('data-sub2api-quality')
  wrapper.unmount()
 })
 it.each([false,true])('requires a completed stream for a usable drawing (%s)',async(completed)=>{
  const html='<html><body><svg><circle r="20" /></svg></body></html>'
  const events=[{type:'content',text:html},...(completed?[{type:'test_complete',success:true}]:[])]
  const body=events.map(e=>'data: '+JSON.stringify(e)+'\n\n').join('')
  vi.stubGlobal('fetch',vi.fn(async()=>new Response(body,{status:200})))
  const wrapper=mount(PelicanTestModal,{
   props:{show:true,account:{id:41,name:'Test',platform:'openai',type:'oauth'} as never},
   global:{stubs:{BaseDialog:{template:'<div><slot/><slot name="footer"/></div>'},Icon:true}}
  })
  await flushPromises()
  const button=wrapper.findAll('button').find(b=>b.classes().includes('btn-primary'))!
  await button.trigger('click');await flushPromises()
  expect(wrapper.find('iframe').exists()).toBe(completed)
  if(!completed)expect(wrapper.get('[role="alert"]').text()).toBe('admin.accounts.pelicanTest.incompleteResponse')
  expect(JSON.parse((vi.mocked(fetch).mock.calls[0][1] as RequestInit).body as string).prompt).toContain('HTML')
  wrapper.unmount()
 })
 it('keeps the built-in candy answer as text when rerun manually',async()=>{
  vi.stubGlobal('fetch',vi.fn(async()=>new Response('data: {"type":"content","text":"21"}\n\ndata: {"type":"test_complete","success":true}\n\n',{status:200})))
  const wrapper=mount(PelicanTestModal,{
   props:{show:true,account:{id:41,name:'Test',platform:'openai',type:'oauth'} as never},
   global:{stubs:{BaseDialog:{template:'<div><slot/><slot name="footer"/></div>'},Icon:true}}
  })
  await flushPromises()
  await wrapper.get('textarea').setValue(CANDY_PROMPT)
  await wrapper.findAll('button').find(b=>b.classes().includes('btn-primary'))!.trigger('click')
  await flushPromises()
  expect(wrapper.find('[role="alert"]').exists()).toBe(false)
  expect(wrapper.find('iframe').exists()).toBe(false)
  expect(wrapper.text()).toContain('admin.accounts.pelicanTest.success')
  const payload=JSON.parse((vi.mocked(fetch).mock.calls[0][1] as RequestInit).body as string)
  expect(payload.prompt).toContain('只输出最终整数')
  expect(payload.prompt).not.toContain('只输出 HTML')
  wrapper.unmount()
 })
})
