class HealthMonitor {
	constructor() {
		this.baseUrl = window.location.origin;
		this.refreshInterval = 5000;
		this.lastUpdate = new Date();
		this.init();
	}

	async init() {
		await this.updateAll();
		setInterval(() => this.updateAll(), this.refreshInterval);
		this.updateRefreshInfo();
	}

	async updateAll() {
		try {
			await Promise.all([
				this.updateStatusCards(),
				this.updateServiceList(),
				this.updateServicePerformance(),
				this.updateCheckVolume(),
				this.updateConfiguration()
			]);
			this.lastUpdate = new Date();
			this.updateRefreshInfo();
		} catch (error) {
			console.error('Error updating dashboard:', error);
			this.showError('Failed to update dashboard: ' + error.message);
		}
	}

	async updateStatusCards() {
		try {
			const response = await fetch(this.baseUrl + '/health/metrics');
			const data = await response.json();

			document.getElementById('monitor-value').textContent = data.monitor_enabled ? 'ON' : 'OFF';
			document.getElementById('total-value').textContent = data.total_services;
			document.getElementById('healthy-value').textContent = data.healthy_services;
			document.getElementById('unhealthy-value').textContent = data.unhealthy_services;

			document.getElementById('monitor-status').className = 'status-card ' +
				(data.monitor_enabled ? 'healthy' : 'unhealthy');
			document.getElementById('total-services').className = 'status-card neutral';
			document.getElementById('healthy-services').className = 'status-card healthy';
			document.getElementById('unhealthy-services').className = 'status-card unhealthy';

			// Update health check status indicator
			this.updateHealthCheckStatus(data);

		} catch (error) {
			console.error('Error updating status cards:', error);
		}
	}

	updateHealthCheckStatus(data) {
		const healthCheckStatus = document.getElementById('health-check-status');
		if (!healthCheckStatus) return;

		if (data.monitor_enabled && data.total_services > 0) {
			const servicesWithData = data.response_times?.services_with_data || 0;
			if (servicesWithData > 0) {
				healthCheckStatus.innerHTML =
					'<div style="color: #4ade80; font-size: 1.1rem;">🔄 Active</div>' +
					'<div style="font-size: 0.9rem; opacity: 0.8;">' +
					servicesWithData + ' services monitored</div>';
				healthCheckStatus.className = 'status-card healthy';
			} else {
				healthCheckStatus.innerHTML =
					'<div style="color: #fbbf24; font-size: 1.1rem;">⏳ Waiting</div>' +
					'<div style="font-size: 0.9rem; opacity: 0.8;">' +
					data.total_services + ' services registered</div>';
				healthCheckStatus.className = 'status-card neutral';
			}
		} else if (data.monitor_enabled) {
			healthCheckStatus.innerHTML =
				'<div style="color: #60a5fa; font-size: 1.1rem;">📝 Ready</div>' +
				'<div style="font-size: 0.9rem; opacity: 0.8;">No services yet</div>';
			healthCheckStatus.className = 'status-card neutral';
		} else {
			healthCheckStatus.innerHTML =
				'<div style="color: #f87171; font-size: 1.1rem;">❌ Disabled</div>' +
				'<div style="font-size: 0.9rem; opacity: 0.8;">Health monitoring off</div>';
			healthCheckStatus.className = 'status-card unhealthy';
		}
	}

	async updateServiceList() {
		try {
			const response = await fetch(this.baseUrl + '/health/status');
			const data = await response.json();

			const serviceList = document.getElementById('service-list');
			serviceList.innerHTML = '';

			if (data.services && data.services.length > 0) {
				data.services.forEach(service => {
					const serviceItem = document.createElement('div');
					serviceItem.className = 'service-item ' + (service.healthy ? 'healthy' : 'unhealthy');

					serviceItem.innerHTML =
						'<div class="service-name">' + service.service + '</div>' +
						'<div class="service-status ' + (service.healthy ? 'healthy' : 'unhealthy') + '">' +
						(service.healthy ? 'HEALTHY' : 'UNHEALTHY') + '</div>';

					serviceList.appendChild(serviceItem);
				});
			} else {
				serviceList.innerHTML =
					'<div style="text-align: center; padding: 20px; opacity: 0.7;">' +
					'No services registered</div>';
			}
		} catch (error) {
			console.error('Error updating service list:', error);
		}
	}

	async updateServicePerformance() {
		try {
			const response = await fetch(this.baseUrl + '/health/status');
			const data = await response.json();

			const performanceMetrics = document.getElementById('performance-metrics');
			performanceMetrics.innerHTML = '';

			if (data.services && data.services.length > 0) {
				// Group services by health status
				const healthyServices = data.services.filter(s => s.healthy);
				const unhealthyServices = data.services.filter(s => !s.healthy);

				let html = '<div style="margin-bottom: 20px;">';

				// Show healthy services with performance data
				if (healthyServices.length > 0) {
					html += '<div style="font-size: 1.1rem; margin-bottom: 15px; ' +
						'color: #4ade80; border-bottom: 1px solid rgba(255,255,255,0.2); ' +
						'padding-bottom: 8px;">✅ Healthy Services</div>';
					healthyServices.forEach(service => {
						const responseTime = service.response_time || 0;
						const lastChecked = new Date(service.last_checked).toLocaleTimeString();
						const isRealRequest = service.response_time_source === 'real_request' || responseTime > 0;

						html += '<div style="display: flex; justify-content: space-between; ' +
							'align-items: center; padding: 8px; margin: 5px 0; ' +
							'background: rgba(74, 222, 128, 0.1); border-radius: 8px; ' +
							'border-left: 4px solid #4ade80;">';
						html += '<div><strong>' + service.service + '</strong><br>' +
							'<small>Last: ' + lastChecked + '</small></div>';
						html += '<div style="text-align: right;"><span style="color: #4ade80; ' +
							'font-weight: bold;">' + responseTime + 'ms</span><br>' +
							'<small>' + (isRealRequest ? '🚀 Real Request' : '🏥 Health Check') +
							'</small></div>';
						html += '</div>';
					});
				}

				// Show unhealthy services with error info
				if (unhealthyServices.length > 0) {
					html += '<div style="font-size: 1.1rem; margin-bottom: 15px; ' +
						'color: #f87171; border-bottom: 1px solid rgba(255,255,255,0.2); ' +
						'padding-bottom: 8px; margin-top: 20px;">❌ Unhealthy Services</div>';
					unhealthyServices.forEach(service => {
						const failureCount = service.failure_count || 0;
						const lastChecked = new Date(service.last_checked).toLocaleTimeString();
						const errorMsg = service.error_message || 'Unknown error';
						const responseTime = service.response_time || 0;
						const isRealRequest = service.response_time_source === 'real_request' || responseTime > 0;

						html += '<div style="display: flex; justify-content: space-between; ' +
							'align-items: center; padding: 8px; margin: 5px 0; ' +
							'background: rgba(248, 113, 113, 0.1); border-radius: 8px; ' +
							'border-left: 4px solid #f87171;">';
						html += '<div><strong>' + service.service + '</strong><br>' +
							'<small>Last: ' + lastChecked + ' | Failures: ' + failureCount +
							'</small><br><small style="color: #fca5a5;">' + errorMsg + '</small></div>';
						html += '<div style="text-align: right;"><span style="color: #f87171; ' +
							'font-weight: bold;">' + responseTime + 'ms</span><br>' +
							'<small>' + (isRealRequest ? '🚀 Real Request' : '🏥 Health Check') +
							'</small></div>';
						html += '</div>';
					});
				}

				html += '</div>';

				// Add performance summary
				const totalServices = data.services.length;
				const servicesWithRealData = data.services.filter(s => s.response_time > 0).length;
				const avgResponseTime = data.services.reduce((sum, s) => sum + (s.response_time || 0), 0) / totalServices;

				html += '<div class="performance-summary">';
				html += '<div style="font-size: 1.1rem; margin-bottom: 10px;">📊 Performance Summary</div>';
				html += '<div style="display: flex; justify-content: space-around; flex-wrap: wrap; gap: 10px;">';
				html += '<div><strong>' + totalServices + '</strong><br><small>Total Services</small></div>';
				html += '<div><strong>' + healthyServices.length + '</strong><br><small>Healthy</small></div>';
				html += '<div><strong>' + unhealthyServices.length + '</strong><br><small>Unhealthy</small></div>';
				html += '<div><strong>' + Math.round(avgResponseTime) + 'ms</strong><br><small>Avg Response</small></div>';
				html += '<div><strong>' + servicesWithRealData + '</strong><br><small>Real Traffic</small></div>';
				html += '</div>';
				html += '</div>';

				// Add legend
				html += '<div class="legend">';
				html += '<div style="margin-bottom: 8px;"><strong>Legend:</strong></div>';
				html += '<div>🚀 <strong>Real Request:</strong> Response time from actual user traffic</div>';
				html += '<div>🏥 <strong>Health Check:</strong> Response time from background monitoring</div>';
				html += '<div style="margin-top: 8px; font-style: italic;">' +
					'Real request data provides actual user experience metrics</div>';
				html += '</div>';

				performanceMetrics.innerHTML = html;
			} else {
				performanceMetrics.innerHTML =
					'<div style="text-align: center; padding: 20px; opacity: 0.7;">' +
					'<div style="font-size: 1.2rem; margin-bottom: 10px; color: #fbbf24;">' +
					'No Services Registered</div>' +
					'<div style="font-size: 0.9rem; opacity: 0.7;">' +
					'Service performance data will appear after services are accessed</div>' +
					'</div>' +
					'<div style="text-align: center; opacity: 0.7;">' +
					'💡 Try accessing a service to start monitoring real traffic performance</div>';
			}
		} catch (error) {
			console.error('Error updating service performance:', error);
		}
	}

	async updateCheckVolume() {
		try {
			const response = await fetch(this.baseUrl + '/health/metrics');
			const data = await response.json();

			const volumeMetrics = document.getElementById('volume-metrics');
			volumeMetrics.innerHTML = '';

			if (data.total_services > 0) {
				const healthRatio = ((data.healthy_services / data.total_services) * 100).toFixed(1);

				volumeMetrics.innerHTML =
					'<div style="margin-bottom: 20px;">' +
					'<div style="font-size: 1.2rem; margin-bottom: 10px;">Health Ratio: ' +
					'<span style="color: #4ade80;">' + healthRatio + '%</span></div>' +
					'<div style="font-size: 1.2rem; margin-bottom: 10px;">Check Interval: ' +
					'<span style="color: #60a5fa;">' +
					(data.configuration && data.configuration.check_interval ?
						data.configuration.check_interval : 'N/A') + '</span></div>' +
					'<div style="font-size: 1.2rem;">Max Failures: ' +
					'<span style="color: #f87171;">' +
					(data.configuration && data.configuration.max_failures ?
						data.configuration.max_failures : 'N/A') + '</span></div>' +
					'</div>' +
					'<div style="text-align: center; opacity: 0.7;">' +
					'Health check configuration and ratios</div>';
			}
		} catch (error) {
			console.error('Error updating check volume:', error);
		}
	}

	async updateConfiguration() {
		try {
			const response = await fetch(this.baseUrl + '/health/metrics');
			const data = await response.json();

			const configMetrics = document.getElementById('config-metrics');
			configMetrics.innerHTML = '';

			if (data.configuration) {
				configMetrics.innerHTML =
					'<div style="margin-bottom: 20px;">' +
					'<div style="font-size: 1.1rem; margin-bottom: 8px;">Check Interval: ' +
					'<span style="color: #60a5fa;">' + data.configuration.check_interval + '</span></div>' +
					'<div style="font-size: 1.1rem; margin-bottom: 8px;">Timeout: ' +
					'<span style="color: #f87171;">' + data.configuration.timeout + '</span></div>' +
					'<div style="font-size: 1.1rem; margin-bottom: 8px;">Max Failures: ' +
					'<span style="color: #f87171;">' + data.configuration.max_failures + '</span></div>' +
					'<div style="font-size: 1.1rem;">Monitor Enabled: ' +
					'<span style="color: ' + (data.monitor_enabled ? '#4ade80' : '#f87171') + '">' +
					(data.monitor_enabled ? 'Yes' : 'No') + '</span></div>' +
					'</div>' +
					'<div style="text-align: center; opacity: 0.7;">' +
					'Current health monitor configuration</div>';
			}
		} catch (error) {
			console.error('Error updating configuration:', error);
		}
	}

	updateRefreshInfo() {
		const lastUpdateElement = document.getElementById('last-update');
		const refreshIntervalElement = document.getElementById('refresh-interval');

		lastUpdateElement.textContent = 'Last update: ' + this.lastUpdate.toLocaleTimeString();
		refreshIntervalElement.textContent = (this.refreshInterval / 1000).toString();
	}

	showError(message) {
		const existingError = document.querySelector('.error-message');
		if (existingError) {
			existingError.remove();
		}

		const errorDiv = document.createElement('div');
		errorDiv.className = 'error-message';
		errorDiv.innerHTML =
			'<strong>Error:</strong> ' + message +
			'<br><small>Check if kube-tunnel is running and accessible at ' +
			this.baseUrl + '</small>';

		document.body.insertBefore(errorDiv, document.querySelector('.dashboard-grid'));
	}
}

document.addEventListener('DOMContentLoaded', () => {
	new HealthMonitor();
});

setInterval(() => {
	const statusCards = document.querySelectorAll('.status-card');
	statusCards.forEach(card => {
		card.classList.add('pulse');
		setTimeout(() => card.classList.remove('pulse'), 500);
	});
}, 5000);
