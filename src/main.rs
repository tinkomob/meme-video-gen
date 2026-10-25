use anyhow::Result;
use meme_video_gen::{
    bot,
    config::{self, Config},
    friends::{self, Friends},
    service::Service,
};
use std::{
    sync::Arc,
    time::{Duration, Instant},
};
use tokio_util::sync::CancellationToken;
use tracing_subscriber::{layer::SubscriberExt, util::SubscriberInitExt};
#[tokio::main]
async fn main() -> Result<()> {
    config::load_env();
    let log = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open("errors.log")?;
    tracing_subscriber::registry()
        .with(
            tracing_subscriber::EnvFilter::try_from_default_env().unwrap_or_else(|_| "info".into()),
        )
        .with(tracing_subscriber::fmt::layer())
        .with(
            tracing_subscriber::fmt::layer()
                .with_ansi(false)
                .with_writer(Arc::new(log)),
        )
        .init();
    let cfg = Config::load()?;
    let svc = Service::new(cfg)?;
    let cancel = CancellationToken::new();
    let listener = tokio::net::TcpListener::bind("0.0.0.0:8080").await?;
    tracing::info!("Friends player: http://localhost:8080/friends");
    let mut tasks = tokio::task::JoinSet::new();
    let (s, c) = (svc.clone(), cancel.clone());
    tasks.spawn(async move {
        s.run_schedule(c).await;
    });
    let (s, c) = (svc.clone(), cancel.clone());
    tasks.spawn(async move {
        s.run_indexer(c).await;
    });
    let (s, c) = (svc.clone(), cancel.clone());
    tasks.spawn(async move {
        if let Err(e) = bot::run(s, c).await {
            tracing::error!("Telegram stopped: {e}");
        }
    });
    let (s, c) = (svc.clone(), cancel.clone());
    tasks.spawn(async move {
        watch_resources(s, c).await;
    });
    let router = friends::router(Friends::new(svc.store.clone()));
    let shutdown = cancel.clone();
    let mut web = tokio::spawn(async move {
        axum::serve(listener, router)
            .with_graceful_shutdown(shutdown.cancelled_owned())
            .await
    });
    let web_result = tokio::select! {
        _ = signal() => None,
        _ = cancel.cancelled() => None,
        result = &mut web => Some(result),
        _ = async {
            while let Some(result) = tasks.join_next().await {
                if let Err(error) = result {
                    tracing::error!("background worker failed: {error}");
                    break;
                }
            }
        } => None,
    };
    cancel.cancel();
    tracing::info!("shutting down");
    let graceful = async {
        while tasks.join_next().await.is_some() {}
        match web_result {
            Some(result) => result??,
            None => web.await??,
        }
        Ok::<_, anyhow::Error>(())
    };
    match tokio::time::timeout(Duration::from_secs(10), graceful).await {
        Ok(result) => result?,
        Err(_) => tracing::warn!("shutdown timed out"),
    }
    Ok(())
}
async fn signal() {
    #[cfg(unix)]
    {
        let mut terminate =
            tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
                .expect("SIGTERM handler");
        tokio::select! {_=tokio::signal::ctrl_c()=>{},_=terminate.recv()=>{}}
    }
    #[cfg(not(unix))]
    {
        let _ = tokio::signal::ctrl_c().await;
    }
}
async fn watch_resources(svc: Service, cancel: CancellationToken) {
    let mut system = sysinfo::System::new();
    let pid = sysinfo::Pid::from_u32(std::process::id());
    let mut warned = None::<Instant>;
    loop {
        tokio::select! {
            _ = cancel.cancelled() => break,
            _ = tokio::time::sleep(Duration::from_secs(30)) => {}
        }
        system.refresh_processes(sysinfo::ProcessesToUpdate::Some(&[pid]), true);
        let memory = system.process(pid).map(|p| p.memory()).unwrap_or(0);
        let tasks = tokio::runtime::Handle::current()
            .metrics()
            .num_alive_tasks();
        let critical = memory >= 1200 * 1024 * 1024 || tasks >= 1000;
        let warn = memory > 600 * 1024 * 1024 || tasks >= 500;
        if critical || (warn && warned.is_none_or(|t| t.elapsed() > Duration::from_secs(600))) {
            let text = format!(
                "{} Ресурсы: память {} MB, задач {tasks}",
                if critical {
                    "🚨 Экстренная остановка."
                } else {
                    "⚠️ Высокое потребление ресурсов."
                },
                memory / (1024 * 1024)
            );
            tracing::warn!("{text}");
            if let Ok(chat) = svc.posts_chat().await
                && chat != 0
            {
                let _ = svc.telegram.text(chat, &text, None).await;
            }
            warned = Some(Instant::now());
            if critical {
                cancel.cancel();
                break;
            }
        }
    }
}
