import { mount, flushPromises } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import PelicanTestModal from '../PelicanTestModal.vue'

vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api/admin/accounts',()=>({getAvailableModels:vi.fn(async()=>[{id:'gpt-6-astra'}])}))
vi.mock('../PelicanScheduledTestsPanel.vue',()=>({default:{template:'<div />'}}))
vi.mock('../PelicanRecordsDashboard.vue',()=>({default:{template:'<div />'}}))

describe('PelicanTestModal',()=>{
 afterEach(()=>{vi.unstubAllGlobals();localStorage.clear()})
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
})
