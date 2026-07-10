import { createRouter, createWebHashHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

// Layout
import DashboardLayout from '@/components/layout/DashboardLayout.vue'

// Pages
import Dashboard from '@/views/dashboard/DashboardView.vue'

// Auth
import LoginView from '@/views/auth/LoginView.vue'
import RegisterView from '@/views/auth/RegisterView.vue'

// Announcements
import AnnouncementList from '@/views/announcement/AnnouncementListView.vue'
import AnnouncementForm from '@/views/announcement/AnnouncementFormView.vue'

// Articles
import ArticleList from '@/views/article/ArticleListView.vue'
import ArticleForm from '@/views/article/ArticleFormView.vue'
import ArticlePreview from '@/views/article/ArticlePreviewView.vue'
import ReviewList from '@/views/article/ReviewListView.vue'
import ReviewDetail from '@/views/article/ReviewDetailView.vue'

// RBAC
import RoleList from '@/views/rbac/RoleListView.vue'
import PermissionList from '@/views/rbac/PermissionListView.vue'

// User
import ProfileView from '@/views/user/ProfileView.vue'

const routes = [
  {
    path: '/login',
    name: '登录',
    component: LoginView,
    meta: { guest: true }
  },
  {
    path: '/register',
    name: '注册',
    component: RegisterView,
    meta: { guest: true }
  },
  {
    path: '/',
    component: DashboardLayout,
    redirect: '/dashboard',
    children: [
      { path: 'dashboard', name: '仪表盘', component: Dashboard },

      // Notices
      { path: 'notices', name: '公告管理', component: AnnouncementList },
      { path: 'notices/new', name: '新建公告', component: AnnouncementForm, meta: { requiresAuth: true, requiresAdmin: true } },
      { path: 'notices/:id/edit', name: '编辑公告', component: AnnouncementForm, meta: { requiresAuth: true, requiresAdmin: true } },

      // Articles
      { path: 'articles', name: '文章中心', component: ArticleList },
      { path: 'articles/new', name: '发布文章', component: ArticleForm, meta: { requiresAuth: true } },
      { path: 'articles/:id', name: '文章阅读', component: ArticlePreview },
      { path: 'articles/:id/edit', name: '编辑文章', component: ArticleForm, meta: { requiresAuth: true } },

      // RBAC (仅超级管理员)
      { path: 'roles', name: '角色管理', component: RoleList, meta: { requiresAuth: true, requiresRbac: true } },
      { path: 'permissions', name: '权限管理', component: PermissionList, meta: { requiresAuth: true, requiresRbac: true } },

      // Reviews (admin+)
      { path: 'reviews', name: '文章审核', component: ReviewList, meta: { requiresAuth: true, requiresAdmin: true } },
      { path: 'reviews/:id', name: '审核详情', component: ReviewDetail, meta: { requiresAuth: true, requiresAdmin: true } },

      // Profile
      { path: 'profile', name: '个人中心', component: ProfileView, meta: { requiresAuth: true } }
    ]
  },
  { path: '/:pathMatch(.*)*', redirect: '/dashboard' }
]

const router = createRouter({
  history: createWebHashHistory(),
  routes,
  linkExactActiveClass: 'active'
})

// Navigation Guards
router.beforeEach((to) => {
  const auth = useAuthStore()
  if (to.meta.guest && auth.isLoggedIn) return '/dashboard'
  if (to.meta.requiresAuth && !auth.isLoggedIn) return '/login'
  if (to.meta.requiresAdmin && !auth.isAdmin) return '/dashboard'
  if (to.meta.requiresRbac && !auth.canManageRbac) return '/dashboard'
})

export default router
