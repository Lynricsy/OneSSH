import React from 'react'
import ReactDOM from 'react-dom/client'
import { ConfigProvider, theme } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import App from './App'
import './styles.css'

ReactDOM.createRoot(document.getElementById('root')!).render(<React.StrictMode><ConfigProvider locale={zhCN} theme={{algorithm:theme.darkAlgorithm,token:{colorPrimary:'#36cfc9',borderRadius:10,colorBgBase:'#07111f',fontFamily:"Inter, ui-sans-serif, system-ui, sans-serif"}}}><App/></ConfigProvider></React.StrictMode>)
