#include <QApplication>
#include <QWidget>
#include <QLabel>
#include <QTimer>
#include <QVBoxLayout>
#include <QDateTime>

class ClockWidget : public QWidget {
    Q_OBJECT
public:
    ClockWidget(QWidget *parent = nullptr) : QWidget(parent) {
        setWindowTitle("Simple Clock");
        setFixedSize(300, 100);

        label = new QLabel(this);
        label->setAlignment(Qt::AlignCenter);
        QFont f = label->font();
        f.setPointSize(24);
        label->setFont(f);

        QVBoxLayout *layout = new QVBoxLayout(this);
        layout->addWidget(label);
        setLayout(layout);

        timer = new QTimer(this);
        connect(timer, &QTimer::timeout, this, &ClockWidget::updateTime);
        timer->start(1000);
        updateTime();
    }

private slots:
    void updateTime() {
        QDateTime current = QDateTime::currentDateTime();
        QString timeText = current.toString("hh:mm:ss AP");
        label->setText(timeText);
    }

private:
    QLabel *label;
    QTimer *timer;
};

int main(int argc, char *argv[]) {
    QApplication app(argc, argv);
    ClockWidget w;
    w.show();
    return app.exec();
}

#include "Clock.moc"
