CC = arm-linux-gnueabihf-gcc
CFLAGS = -fPIC -shared -O2 -Wall
LDFLAGS = -ldl -lpthread

TARGET = libdirectpath.so
SRC = direct_path.c

all: $(TARGET)

$(TARGET): $(SRC)
	@echo "[BUILD] Compiling $(TARGET) for ARMv7 via docker arm-builder..."
	docker run --rm -v $(CURDIR):/work -w /work arm-builder $(CC) $(CFLAGS) $(SRC) -o $(TARGET) $(LDFLAGS)
	@echo "[BUILD] Done. Output: $(TARGET)"

clean:
	rm -f $(TARGET)
