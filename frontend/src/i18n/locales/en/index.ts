import landing from './landing'
import common from './common'
import dashboard from './dashboard'
import channelMonitorV2 from './channelMonitorV2'
import batchImage from './batchImage'
import admin from './admin'
import pelicanAccount from './pelicanAccount'
import pelicanShowcase from './pelicanShowcase'
import pelicanTests from './pelicanTests'
import misc from './misc'

export default {
  ...landing,
  ...common,
  ...dashboard,
  ...channelMonitorV2,
  ...batchImage,
  admin: {...admin,accounts:{...admin.accounts,...pelicanAccount}},
  ...pelicanShowcase,
  pelicanTests,
  ...misc,
}
