# Discover all immediate subdirectories containing a Makefile
SUBDIRS := $(patsubst %/Makefile,%,$(wildcard */Makefile))

all: $(SUBDIRS)
	@echo "[SUCCESS] All discovered patches compiled: $(SUBDIRS)"

$(SUBDIRS):
	@echo "=== Building $@ ==="
	@$(MAKE) -C $@ all

clean:
	@for dir in $(SUBDIRS); do \
		$(MAKE) -C $$dir clean; \
	done

.PHONY: all clean $(SUBDIRS)
