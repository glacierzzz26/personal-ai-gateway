// dayjs 全局配置:UTC 插件 + 中文 locale。main.tsx 最先 import 本模块保证先于业务代码生效。
import dayjs from 'dayjs'
import utc from 'dayjs/plugin/utc'
import 'dayjs/locale/zh-cn'

dayjs.extend(utc)
dayjs.locale('zh-cn')

export default dayjs
