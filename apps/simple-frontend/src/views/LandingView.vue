<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ArrowRight, BookOpenText, Feather, Search, ShieldCheck, Sparkles } from '@lucide/vue'
import AppLogo from '@/components/AppLogo.vue'
import { healthApi } from '@/api/health'

const serviceStatus = ref<'checking' | 'ready' | 'offline'>('checking')

onMounted(async () => {
  try {
    await healthApi.ready()
    serviceStatus.value = 'ready'
  } catch {
    serviceStatus.value = 'offline'
  }
})
</script>

<template>
  <div class="landing">
    <header class="landing__nav container">
      <AppLogo />
      <nav>
        <a href="#features">能力</a>
        <RouterLink :to="{ name: 'manager-login' }">管理入口</RouterLink>
      </nav>
      <div class="landing__actions">
        <RouterLink :to="{ name: 'login' }" class="button button--ghost">登录</RouterLink>
        <RouterLink :to="{ name: 'register' }" class="button button--dark">免费开始<ArrowRight :size="16" /></RouterLink>
      </div>
    </header>

    <main>
      <section class="hero container">
        <div class="hero__copy">
          <div class="eyebrow"><Sparkles :size="15" />为想法留一片安静的地方</div>
          <h1>写下所想，<br /><em>轻盈地分享。</em></h1>
          <p>一个专注 Markdown 创作、阅读与轻量交流的工作空间。没有喧嚣，只留下真正重要的文字。</p>
          <div class="hero__actions">
            <RouterLink :to="{ name: 'register' }" class="button button--primary button--large">创建你的空间<ArrowRight :size="18" /></RouterLink>
            <RouterLink :to="{ name: 'login' }" class="button button--soft button--large">已有账号</RouterLink>
          </div>
          <div class="service-indicator" :class="`service-indicator--${serviceStatus}`">
            <span />
            {{ serviceStatus === 'checking' ? '正在检查正式文档服务' : serviceStatus === 'ready' ? '正式文档服务运行正常' : '正式文档服务暂未就绪' }}
          </div>
        </div>

        <div class="hero__visual" aria-label="Paperplane 编辑器预览">
          <div class="paper-card paper-card--back"><span>PUBLIC</span></div>
          <div class="paper-card paper-card--main">
            <div class="paper-card__toolbar"><i /><i /><i /><span>我的第一篇文稿.md</span></div>
            <div class="paper-card__content">
              <span class="paper-card__kicker">01 / NOTES</span>
              <h2>保持好奇，<br />也保持清醒。</h2>
              <p>我们用文字保存那些一闪而过，却值得被记住的瞬间。</p>
              <div class="paper-lines"><i /><i /><i /><i /></div>
            </div>
            <span class="paper-card__plane"><Feather :size="28" /></span>
          </div>
        </div>
      </section>

      <section id="features" class="features container">
        <div class="section-heading">
          <span>BUILT FOR FOCUS</span>
          <h2>从一个念头，到一篇作品</h2>
        </div>
        <div class="feature-grid">
          <article><span><BookOpenText /></span><h3>沉浸创作</h3><p>Markdown 编辑与实时预览并排呈现，让格式退到文字之后。</p></article>
          <article><span><Sparkles /></span><h3>发现好文字</h3><p>在公开广场浏览社区内容，也可以随时让自己的文章保持私密。</p></article>
          <article><span><Search /></span><h3>快速检索</h3><p>按标题与正文关键词检索公开或自己的文章，让积累的内容随时可找回。</p></article>
          <article><span><ShieldCheck /></span><h3>清晰权限</h3><p>普通用户与管理员身份完全隔离，审批过程可追踪且可复核。</p></article>
        </div>
      </section>
    </main>

    <footer class="landing__footer container"><AppLogo /><span>为清晰思考而造 · Paperplane</span></footer>
  </div>
</template>
