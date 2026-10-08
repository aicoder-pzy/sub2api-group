export default {
pelicanShowcase: {
    title: '鹈鹕测智',
    description: '各分组的模型定时完成同一道绘图题，直接看生成的作品，直观比较模型水平',
    allGroups: '全部分组',
    keepRule: '每组保留最近 {count} 张',
    retentionRule: '超过 {days} 天自动清理',
    itemCount: '{count} 张',
    latestAt: '最近更新 {time}',
    groupEmpty: '该分组还没有作品，定时测试成功生成后会出现在这里',
    scrollLabel: '{group}：拖动滑块查看更早的作品',
    loadError: '加载鹈鹕测智失败',
    itemLoading: '作品加载中…',
    itemLoadError: '作品加载失败',
    invalidHtml: '这张作品无法显示',
    duration: '耗时 {seconds} 秒',
    reasoning: '思考强度 {effort}',
    efforts: {
      minimal: '最低',
      low: '低',
      medium: '中',
      high: '高',
      xhigh: '极高'
    },
    preview: '查看大图',
    previewTitle: '{group} · {model}',
    fitArtwork: '适应窗口',
    actualSize: '100%',
    previewSizing: '预览缩放',
    sandboxNote: '作品在隔离沙箱中运行，不能联网，也读取不到你的账号信息。',
    remove: '从展示中移除',
    removeConfirm: '确定把这张作品从鹈鹕测智中移除吗？移除后所有用户都看不到它，此操作不能撤销。',
    removed: '已从展示中移除',
    removeFailed: '移除失败',
    api: {
      title: 'API 调用',
      available: 'API 已开放',
      unavailable: 'API 未开放',
      unavailableHint: '管理员尚未开放 API Key 读取作品，当前无法调用。以下为开放后的调用方式。',
      manageKeys: '管理 API Key',
      readOnly: '免费只读接口，读取已发布的成功作品，不发起测试、不调用模型、不扣 API Key 余额。',
      manifestEndpoint: '作品清单',
      itemEndpoint: '作品正文',
      copyUrl: '复制接口地址',
      authentication: '使用本站有效 API Key，通过 Authorization: Bearer YOUR_API_KEY 鉴权。网页登录令牌不可用于此接口。',
      examples: '调用示例',
      manifestExample: '读取清单',
      itemExample: '读取正文',
      cacheExample: '条件请求',
      copyExample: '复制调用示例',
      manifestHint: 'data.groups 包含各分组的作品摘要。清单不含完整 HTML/SVG，按作品 content_url 获取正文。',
      itemHint: '作品 ID 以清单为准；没有作品时请替换 RESULT_ID。data.response_text 是原始输出，可能含代码围栏。作品移除或过期后返回 404。',
      cacheHint: '将 YOUR_ETAG 替换为上次响应 ETag 的完整原值，包括 W/ 和双引号。返回 304 时沿用本地清单；返回 200 时只下载尚未保存的作品。',
      polling: '建议每 60 秒或更久轮询，使用 ETag 和 If-None-Match 检查变化。清单和正文均支持 GET/HEAD；遇到 429/503 请遵守 Retry-After。',
      keySafety: '建议在调用端后端保存 API Key，避免放入公开前端。跨域浏览器请求应不携带 Cookie；显示作品时使用隔离 iframe。'
    },
    disabled: {
      title: '鹈鹕测智暂未开放',
      description: '管理员开启后，这里会展示各分组定时生成的作品。'
    },
    empty: {
      title: '暂无作品',
      description: '管理员还没有选择要展示的分组。'
    }
  }
}
