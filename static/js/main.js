class VideoGenerator {
    constructor() {
        this.currentVideoData = null;
        this.isGenerating = false;
        this.isPublishing = false;
        this.initializeEventListeners();
    }

    initializeEventListeners() {
        const generateForm = document.getElementById('generateForm');
        if (generateForm) {
            generateForm.addEventListener('submit', (e) => {
                e.preventDefault();
                if (!this.isGenerating) {
                    this.generateVideo();
                }
            });
        }

        const regenerateBtn = document.getElementById('regenerateBtn');
        if (regenerateBtn) {
            regenerateBtn.addEventListener('click', (e) => {
                e.preventDefault();
                if (!this.isGenerating) {
                    this.generateVideo();
                }
            });
        }

        const publishBtn = document.getElementById('publishBtn');
        if (publishBtn) {
            publishBtn.addEventListener('click', () => this.showPublishModal());
        }

        const cancelPublishBtn = document.getElementById('cancelPublishBtn');
        if (cancelPublishBtn) {
            cancelPublishBtn.addEventListener('click', () => this.hidePublishModal());
        }

        const confirmPublishBtn = document.getElementById('confirmPublishBtn');
        if (confirmPublishBtn) {
            confirmPublishBtn.addEventListener('click', () => this.publishVideo());
        }

        document.addEventListener('keydown', (e) => {
            if (e.key === 'Escape') {
                this.hidePublishModal();
                this.hideResultSection();
                this.hidePublishProgress();
                this.hidePublishResults();
            }
        });

        // Обработчики клика вне оверлеев для их закрытия
        document.addEventListener('click', (e) => {
            const publishModal = document.getElementById('publishModal');
            const resultSection = document.getElementById('resultSection');
            const publishProgress = document.getElementById('publishProgress');
            const publishResults = document.getElementById('publishResults');

            if (publishModal && !publishModal.classList.contains('hidden') && !publishModal.contains(e.target)) {
                this.hidePublishModal();
            }
            if (resultSection && !resultSection.classList.contains('hidden') && !resultSection.contains(e.target)) {
                this.hideResultSection();
            }
            if (publishProgress && !publishProgress.classList.contains('hidden') && !publishProgress.contains(e.target)) {
                this.hidePublishProgress();
            }
            if (publishResults && !publishResults.classList.contains('hidden') && !publishResults.contains(e.target)) {
                this.hidePublishResults();
            }
        });
    }

    async generateVideo() {
        if (this.isGenerating) return;
        
        const generateBtn = document.getElementById('generateBtn');
        
        if (!generateBtn) return;

        this.isGenerating = true;
        this.hideResultSection();
        this.hidePublishProgress();
        this.hidePublishResults();
        generateBtn.disabled = true;
        generateBtn.innerHTML = '<i class="fas fa-spinner fa-spin mr-2"></i>Генерация...';
        
        try {
            const formData = this.getFormData();
            
            const response = await fetch('/generate', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify(formData)
            });
            
            if (!response.ok) {
                const errorData = await response.json().catch(() => ({}));
                throw new Error(errorData.detail || `HTTP error! status: ${response.status}`);
            }
            
            const result = await response.json();
            this.currentVideoData = result;
            
            setTimeout(() => {
                this.currentVideoData = result;
                this.showResult(result);
            }, 500);
            
        } catch (error) {
            console.error('Error generating video:', error);
            this.showNotification('Ошибка генерации видео: ' + error.message, 'error');
        } finally {
            this.isGenerating = false;
            generateBtn.disabled = false;
            generateBtn.innerHTML = '<i class="fas fa-magic mr-2"></i>Сгенерировать';
        }
    }

    getFormData() {
        return {
            pinterest_urls: null,
            music_playlists: null,
            pin_num: 1000,
            audio_duration: 10
        };
    }

    updateProgress(percent, text) {
        const progressText = document.getElementById('progressText');
        
        if (progressText) progressText.textContent = text;
    }

    showResult(result) {
        if (result.video_path) {
            const previewVideo = document.getElementById('previewVideo');
            const resultSection = document.getElementById('resultSection');
            
            if (previewVideo) previewVideo.src = '/video/' + encodeURIComponent(result.video_path);
            if (resultSection) resultSection.classList.remove('hidden');
            
            this.showNotification('Видео успешно сгенерировано!', 'success');
        } else {
            this.showNotification('Не удалось сгенерировать видео. Попробуйте еще раз.', 'error');
        }
    }

    hideResultSection() {
        const resultSection = document.getElementById('resultSection');
        if (resultSection) {
            resultSection.classList.add('hidden');
        }
    }

    hidePublishProgress() {
        const publishProgress = document.getElementById('publishProgress');
        if (publishProgress) {
            publishProgress.classList.add('hidden');
        }
    }

    hidePublishResults() {
        const publishResults = document.getElementById('publishResults');
        if (publishResults) {
            publishResults.classList.add('hidden');
        }
    }

    async publishVideo() {
        if (!this.currentVideoData) return;
        
        this.hidePublishModal();
        this.hidePublishResults();
        
        const publishProgress = document.getElementById('publishProgress');
        const publishResults = document.getElementById('publishResults');
        
        publishResults?.classList.add('hidden');
        publishProgress?.classList.remove('hidden');
        
        const platforms = this.getSelectedPlatforms();
        const privacy = document.getElementById('privacy')?.value || 'public';
        const dryRun = document.getElementById('dryRun')?.checked || false;
        
        this.updatePublishStatus(platforms, 'Начинается публикация...');
        
        try {
            const response = await fetch('/deploy', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({
                    video_path: this.currentVideoData.video_path,
                    thumbnail_path: this.currentVideoData.thumbnail_path,
                    source_url: this.currentVideoData.source_url,
                    privacy: privacy,
                    socials: platforms,
                    dry_run: dryRun
                })
            });
            
            if (!response.ok) {
                const errorData = await response.json().catch(() => ({}));
                throw new Error(errorData.detail || `HTTP error! status: ${response.status}`);
            }
            
            const result = await response.json();
            
            setTimeout(() => {
                publishProgress?.classList.add('hidden');
                this.showPublishResults(result.deployment_links);
            }, 1000);
            
        } catch (error) {
            console.error('Error publishing video:', error);
            this.updatePublishStatus([], 'Ошибка публикации: ' + error.message);
            this.showNotification('Ошибка публикации: ' + error.message, 'error');
            setTimeout(() => {
                this.hidePublishProgress();
            }, 3000);
        }
    }

    getSelectedPlatforms() {
        const platforms = [];
        if (document.getElementById('platformYoutube')?.checked) platforms.push('youtube');
        if (document.getElementById('platformInstagram')?.checked) platforms.push('instagram');
        if (document.getElementById('platformTiktok')?.checked) platforms.push('tiktok');
        if (document.getElementById('platformX')?.checked) platforms.push('x');
        return platforms;
    }

    updatePublishStatus(platforms, message) {
        const statusDiv = document.getElementById('publishStatus');
        if (!statusDiv) return;
        
        statusDiv.innerHTML = `<p class="text-gray-600">${message}</p>`;
        
        platforms.forEach(platform => {
            statusDiv.innerHTML += `
                <div class="flex items-center">
                    <i class="fas fa-spinner fa-spin text-primary mr-2"></i>
                    <span class="capitalize">${platform}</span>
                </div>
            `;
        });
    }

    showPublishResults(links) {
        const resultsDiv = document.getElementById('publishResults');
        const linksDiv = document.getElementById('publishLinks');
        
        if (!resultsDiv || !linksDiv) return;
        
        linksDiv.innerHTML = '';
        
        for (const [platform, link] of Object.entries(links || {})) {
            if (link) {
                const icon = this.getPlatformIcon(platform);
                linksDiv.innerHTML += `
                    <div class="flex items-center justify-between p-3 bg-green-50 rounded-lg">
                        <div class="flex items-center">
                            <i class="${icon} mr-2"></i>
                            <span class="capitalize font-medium">${platform}</span>
                        </div>
                        <a href="${link}" target="_blank" class="text-blue-600 hover:text-blue-800 underline">
                            Открыть
                            <i class="fas fa-external-link-alt ml-1"></i>
                        </a>
                    </div>
                `;
            } else {
                const icon = this.getPlatformIcon(platform);
                linksDiv.innerHTML += `
                    <div class="flex items-center justify-between p-3 bg-red-50 rounded-lg">
                        <div class="flex items-center">
                            <i class="${icon} mr-2"></i>
                            <span class="capitalize font-medium">${platform}</span>
                        </div>
                        <span class="text-red-600">Ошибка публикации</span>
                    </div>
                `;
            }
        }
        
        resultsDiv.classList.remove('hidden');
        this.showNotification('Публикация завершена!', 'success');
        
        // Автоматически закрыть через 10 секунд
        setTimeout(() => {
            this.hidePublishResults();
        }, 10000);
    }

    getPlatformIcon(platform) {
        const icons = {
            youtube: 'fab fa-youtube text-red-500',
            instagram: 'fab fa-instagram text-pink-500',
            tiktok: 'fab fa-tiktok text-black',
            x: 'fab fa-x-twitter text-black'
        };
        return icons[platform] || 'fas fa-share';
    }

    showNotification(message, type = 'info') {
        const notification = document.createElement('div');
        const bgColor = type === 'error' ? 'bg-red-500' : type === 'success' ? 'bg-green-500' : 'bg-blue-500';
        
        notification.className = `fixed top-4 right-4 ${bgColor} text-white px-6 py-3 rounded-lg shadow-lg z-50 transform translate-x-full transition-transform duration-300`;
        notification.innerHTML = `
            <div class="flex items-center">
                <i class="fas ${type === 'error' ? 'fa-exclamation-triangle' : type === 'success' ? 'fa-check-circle' : 'fa-info-circle'} mr-2"></i>
                <span>${message}</span>
                <button onclick="this.parentElement.parentElement.remove()" class="ml-4 text-white hover:text-gray-200">
                    <i class="fas fa-times"></i>
                </button>
            </div>
        `;
        
        document.body.appendChild(notification);
        
        setTimeout(() => {
            notification.classList.remove('translate-x-full');
        }, 100);
        
        setTimeout(() => {
            notification.classList.add('translate-x-full');
            setTimeout(() => {
                notification.remove();
            }, 300);
        }, 5000);
    }
}

class HistoryManager {
    constructor() {
        this.initializeEventListeners();
    }

    initializeEventListeners() {
        const refreshBtn = document.getElementById('refreshBtn');
        if (refreshBtn) {
            refreshBtn.addEventListener('click', () => this.loadHistory());
        }
        
        if (window.location.pathname === '/history') {
            this.loadHistory();
        }
    }

    async loadHistory() {
        const historyList = document.getElementById('historyList');
        if (!historyList) return;
        
        historyList.innerHTML = `
            <div class="text-center py-8 text-gray-500">
                <i class="fas fa-spinner fa-spin text-2xl mb-4"></i>
                <p>Загрузка истории...</p>
            </div>
        `;
        
        try {
            const response = await fetch('/api/history');
            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }
            
            const history = await response.json();
            this.displayHistory(history);
            
        } catch (error) {
            console.error('Error loading history:', error);
            historyList.innerHTML = `
                <div class="text-center py-8 text-red-500">
                    <i class="fas fa-exclamation-triangle text-2xl mb-4"></i>
                    <p>Ошибка загрузки истории: ${error.message}</p>
                </div>
            `;
        }
    }

    displayHistory(history) {
        const historyList = document.getElementById('historyList');
        if (!historyList) return;
        
        if (!history || history.length === 0) {
            historyList.innerHTML = `
                <div class="text-center py-8 text-gray-500">
                    <i class="fas fa-video-slash text-2xl mb-4"></i>
                    <p>История пуста. Создайте первое видео!</p>
                    <a href="/" class="inline-block mt-4 bg-primary hover:bg-blue-600 text-white font-bold py-2 px-4 rounded-lg transition-colors duration-200">
                        <i class="fas fa-plus mr-2"></i>Создать видео
                    </a>
                </div>
            `;
            return;
        }
        
        historyList.innerHTML = history.map(item => this.renderHistoryItem(item)).join('');
    }

    renderHistoryItem(item) {
        return `
            <div class="border border-gray-200 rounded-lg p-4 hover:shadow-md transition-shadow">
                <div class="flex flex-col md:flex-row gap-4">
                    <div class="flex-shrink-0">
                        ${item.thumbnail_path ? `
                            <img 
                                src="/thumbnail/${encodeURIComponent(item.thumbnail_path)}" 
                                alt="Миниатюра" 
                                class="w-32 h-24 object-cover rounded-lg"
                            >
                        ` : `
                            <div class="w-32 h-24 bg-gray-200 rounded-lg flex items-center justify-center">
                                <i class="fas fa-image text-gray-400"></i>
                            </div>
                        `}
                    </div>
                    
                    <div class="flex-grow">
                        <div class="flex justify-between items-start mb-2">
                            <h4 class="font-semibold text-gray-800">${item.title || 'Без названия'}</h4>
                            <span class="text-sm text-gray-500">${this.formatDate(item.created_at)}</span>
                        </div>
                        
                        <p class="text-gray-600 text-sm mb-3">
                            <i class="fas fa-link mr-1"></i>
                            Источник: ${item.source_url ? `<a href="${item.source_url}" target="_blank" class="text-blue-600 hover:underline">${this.truncateUrl(item.source_url)}</a>` : 'Не указан'}
                        </p>
                        
                        <div class="flex flex-wrap gap-2 mb-3">
                            ${this.renderPlatformLinks(item.deployment_links)}
                        </div>
                        
                        <div class="flex gap-2">
                            ${item.video_path ? `
                                <button 
                                    onclick="historyManager.previewVideo('${item.video_path}')"
                                    class="bg-blue-500 hover:bg-blue-600 text-white text-xs py-1 px-3 rounded transition-colors"
                                >
                                    <i class="fas fa-play mr-1"></i>Просмотр
                                </button>
                            ` : ''}
                            
                            <button 
                                onclick="historyManager.republishVideo('${item.id}')"
                                class="bg-green-500 hover:bg-green-600 text-white text-xs py-1 px-3 rounded transition-colors"
                            >
                                <i class="fas fa-share mr-1"></i>Переопубликовать
                            </button>
                            
                            <button 
                                onclick="historyManager.deleteVideo('${item.id}')"
                                class="bg-red-500 hover:bg-red-600 text-white text-xs py-1 px-3 rounded transition-colors"
                            >
                                <i class="fas fa-trash mr-1"></i>Удалить
                            </button>
                        </div>
                    </div>
                </div>
            </div>
        `;
    }

    renderPlatformLinks(deploymentLinks) {
        if (!deploymentLinks || Object.keys(deploymentLinks).length === 0) {
            return `
                <span class="inline-flex items-center px-3 py-1 bg-gray-100 text-gray-600 rounded-full text-xs">
                    <i class="fas fa-clock mr-1"></i>
                    Не опубликовано
                </span>
            `;
        }

        return Object.entries(deploymentLinks).map(([platform, link]) => {
            if (!link) return '';
            const icon = this.getPlatformIcon(platform);
            return `
                <a 
                    href="${link}" 
                    target="_blank"
                    class="inline-flex items-center px-3 py-1 bg-green-100 text-green-800 rounded-full text-xs hover:bg-green-200 transition-colors"
                >
                    <i class="${icon} mr-1"></i>
                    ${platform}
                </a>
            `;
        }).join('');
    }

    formatDate(dateString) {
        if (!dateString) return 'Неизвестно';
        try {
            return new Date(dateString).toLocaleString('ru-RU');
        } catch {
            return 'Неизвестно';
        }
    }

    truncateUrl(url, maxLength = 50) {
        if (!url) return 'Не указан';
        return url.length > maxLength ? url.substring(0, maxLength) + '...' : url;
    }

    getPlatformIcon(platform) {
        const icons = {
            youtube: 'fab fa-youtube',
            instagram: 'fab fa-instagram',
            tiktok: 'fab fa-tiktok',
            x: 'fab fa-x-twitter',
            twitter: 'fab fa-x-twitter'
        };
        return icons[platform] || 'fas fa-share';
    }

    previewVideo(videoPath) {
        const modal = document.createElement('div');
        modal.className = 'fixed inset-0 bg-black bg-opacity-75 z-50 flex items-center justify-center p-4';
        modal.innerHTML = `
            <div class="bg-white rounded-lg p-4 max-w-2xl w-full">
                <div class="flex justify-between items-center mb-4">
                    <h3 class="text-lg font-semibold">Просмотр видео</h3>
                    <button onclick="this.closest('.fixed').remove()" class="text-gray-500 hover:text-gray-700">
                        <i class="fas fa-times text-xl"></i>
                    </button>
                </div>
                <video controls class="w-full rounded-lg">
                    <source src="/video/${encodeURIComponent(videoPath)}" type="video/mp4">
                    Ваш браузер не поддерживает видео.
                </video>
            </div>
        `;
        
        document.body.appendChild(modal);
    }

    async republishVideo(videoId) {
        if (!confirm('Вы уверены, что хотите переопубликовать это видео?')) {
            return;
        }
        
        try {
            const response = await fetch(`/api/republish/${videoId}`, {
                method: 'POST'
            });
            
            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }
            
            const result = await response.json();
            alert('Видео успешно переопубликовано!');
            this.loadHistory();
            
        } catch (error) {
            console.error('Error republishing video:', error);
            alert('Ошибка переопубликации: ' + error.message);
        }
    }

    async deleteVideo(videoId) {
        if (!confirm('Вы уверены, что хотите удалить это видео? Это действие нельзя отменить.')) {
            return;
        }
        
        try {
            const response = await fetch(`/api/delete/${videoId}`, {
                method: 'DELETE'
            });
            
            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }
            
            alert('Видео успешно удалено!');
            this.loadHistory();
            
        } catch (error) {
            console.error('Error deleting video:', error);
            alert('Ошибка удаления: ' + error.message);
        }
    }
}

let videoGenerator;
let historyManager;

document.addEventListener('DOMContentLoaded', function() {
    videoGenerator = new VideoGenerator();
    historyManager = new HistoryManager();
});