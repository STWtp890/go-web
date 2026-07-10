import { reactive } from 'vue'

export const sidebarState = reactive({ showSidebar: false })

export function useSidebar() {
  return {
    showSidebar: sidebarState.showSidebar,
    displaySidebar(value) { sidebarState.showSidebar = value },
    toggleSidebar() { sidebarState.showSidebar = !sidebarState.showSidebar }
  }
}

export const SidebarPlugin = {
  install(app) {
    app.config.globalProperties.$sidebar = {
      get showSidebar() { return sidebarState.showSidebar },
      displaySidebar(value) { sidebarState.showSidebar = value }
    }
  }
}
