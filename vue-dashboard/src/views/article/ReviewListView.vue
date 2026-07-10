<template>
  <div>
    <div class="app-page">
      <div class="d-flex justify-content-between align-items-center mb-4">
        <h3 class="mb-0"><i class="icon-check text-warning mr-2" />文章审核</h3>
        <span class="badge badge-warning" v-if="list.length">待审核 {{ list.length }} 篇</span>
      </div>

      <!-- 加载 / 错误 -->
      <div v-if="errorMsg" class="alert alert-danger">{{ errorMsg }}</div>
      <div v-if="loading" class="text-center py-5">
        <div class="spinner-border text-primary" /><p class="mt-2 text-muted">加载中...</p>
      </div>

      <!-- 列表 -->
      <template v-if="!loading">
        <div v-if="list.length === 0" class="empty-state">
          <i class="icon-check empty-state-icon" />
          <p class="empty-state-text">暂无待审核文章 🎉</p>
        </div>

        <div v-for="item in list" :key="item.markdownId" class="list-card mb-3"
             @click="$router.push(`/reviews/${item.markdownId}`)">
          <div class="list-card-body">
            <h5 class="list-card-title">{{ item.title }}</h5>
            <p class="list-card-summary">{{ stripMd(item.summary) || '暂无摘要' }}</p>
            <div class="list-card-footer">
              <span class="list-card-time">提交于 {{ fmt(item.createdAt) }}</span>
              <span class="badge badge-info ml-2">待审核</span>
            </div>
          </div>
        </div>

        <!-- 分页 -->
        <div v-if="totalPages > 1" class="d-flex justify-content-center mt-4">
          <button class="btn btn-sm btn-default" :disabled="page <= 1" @click="page--">上一页</button>
          <span class="mx-3 align-self-center text-muted">第 {{ page }} / {{ totalPages }} 页</span>
          <button class="btn btn-sm btn-default" :disabled="page >= totalPages" @click="page++">下一页</button>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { markdownAPI } from '@/api/markdown'
import { usePagination } from '@/composables/usePagination'
import { useAsync } from '@/composables/useAsync'
import { fmtDate, stripMd } from '@/utils/format'

const list = ref([])
const { loading, errorMsg, run } = useAsync()

async function fetchData(p) {
  const pg = p ?? page.value
  await run(async () => {
    const r = await markdownAPI.review.unreviewed({ page: pg, pageSize: pageSize.value })
    list.value = r.markdownList?.markdownList || []
    totalCount.value = r.totalCount || 0
  }, '加载待审核列表失败')
}

const { page, pageSize, totalCount, totalPages } = usePagination(fetchData)

function fmt(ts) { return fmtDate(ts) }
</script>
