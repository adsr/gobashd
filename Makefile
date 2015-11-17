prefix=/usr/local
exec_prefix=$(prefix)
bindir=$(exec_prefix)/bin

INSTALL=install
BINDIR=$(DESTDIR)$(bindir)
TARGET=gobashd

all: $(TARGET)

gobashd: main.go
	go build -o $(TARGET)

check: gobashd
	./$(TARGET) -v

clean:
	rm -f $(TARGET)

install: gobashd
	$(INSTALL) -D $(TARGET) $(BINDIR)/$(TARGET)

uninstall:
	rm -f $(BINDIR)/$(TARGET)
