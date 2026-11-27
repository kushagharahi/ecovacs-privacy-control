CC = arm-linux-gnueabihf-gcc
CFLAGS = -fPIC -shared -O2 -Wall
LDFLAGS = -ldl -lpthread -lm

TARGET = libfastreloc.so
SRC = fast_reloc.c

all: $(TARGET)

$(TARGET): $(SRC)
	@echo "[BUILD] Compiling $(TARGET) for ARMv7 via docker arm-builder..."
	docker run --rm -v $(CURDIR):/work -w /work arm-builder $(CC) $(CFLAGS) $(SRC) -o $(TARGET) $(LDFLAGS)
	@echo "[BUILD] Done. Output: $(TARGET)"

clean:
	rm -f $(TARGET)
