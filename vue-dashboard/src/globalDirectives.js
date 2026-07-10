/**
 * Vue 3 click-outside directive
 * Usage: v-click-outside="handler"
 */
const clickOutsideDirective = {
  mounted(el, binding) {
    el.__clickOutsideHandler = (event) => {
      if (!(el === event.target || el.contains(event.target))) {
        binding.value(event)
      }
    }
    document.addEventListener('click', el.__clickOutsideHandler)
  },
  unmounted(el) {
    document.removeEventListener('click', el.__clickOutsideHandler)
    delete el.__clickOutsideHandler
  }
}

const GlobalDirectives = {
  install(app) {
    app.directive('click-outside', clickOutsideDirective)
  }
}

export default GlobalDirectives
