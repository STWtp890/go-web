<template>
  <div>
    <div class="app-page">
      <div class="row">
        <div class="col-lg-4" v-for="card in cards" :key="card.title">
        <div class="card">
          <div class="card-body text-center py-4">
            <i :class="card.icon" style="font-size:36px;color:var(--app-brand);" />
            <h4 class="mt-2">{{ card.title }}</h4>
            <p class="text-muted small">{{ card.desc }}</p>
            <router-link :to="card.link" class="btn btn-outline-primary btn-sm mt-2">{{ card.btn }}</router-link>
          </div>
        </div>
      </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const cards = [
  { title: '公告管理', desc: '查看与发布系统公告', icon: 'icon-volume-high', link: '/notices', btn: '查看公告' },
  { title: '文章中心', desc: 'Markdown 文档管理', icon: 'icon-doc-text', link: '/articles', btn: '浏览文章' },
  ...(auth.isAdmin ? [
    { title: '文章审核', desc: '审核待发布的 Markdown', icon: 'icon-check', link: '/reviews', btn: '去审核' }
  ] : []),
  ...(auth.canManageRbac ? [
    { title: '角色管理', desc: 'RBAC 角色与权限', icon: 'icon-tag', link: '/roles', btn: '管理角色' },
    { title: '权限管理', desc: '细粒度权限定义', icon: 'icon-key', link: '/permissions', btn: '管理权限' }
  ] : [])
]
</script>
