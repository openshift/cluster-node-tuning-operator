NAME	:=	stalld
VERSION	:=	1.12

INSTALL	=	install
CC	:=	gcc
FOPTS	:=	-flto=auto -ffat-lto-objects -fexceptions -fstack-protector-strong \
		-fasynchronous-unwind-tables -fstack-clash-protection
WOPTS	:= 	-Wall -Werror=format-security -Wp,-D_FORTIFY_SOURCE=2 -Wp,-D_GLIBCXX_ASSERTIONS
SOPTS	:= 	-specs=/usr/lib/rpm/redhat/redhat-hardened-cc1 -specs=/usr/lib/rpm/redhat/redhat-annobin-cc1

CFLAGS	:=	-O2 -g -DVERSION=\"$(VERSION)\" $(FOPTS) $(WOPTS) $(SOPTS)
LDFLAGS	:=	-ggdb
LIBS	:=	 -lpthread

SRC	:=	$(wildcard src/*.c)
HDR	:=	$(wildcard src/*.h)
OBJ	:=	$(SRC:.c=.o)
DIRS	:=	src redhat man tests scripts
FILES	:=	Makefile README.md gpl-2.0.txt scripts/throttlectl.sh
CEXT	:=	bz2
TARBALL	:=	$(NAME)-$(VERSION).tar.$(CEXT)
TAROPTS	:=	-cvjf $(TARBALL)
BINDIR	:=	/usr/bin
DATADIR	:=	/usr/share
DOCDIR	:=	$(DATADIR)/doc
MANDIR	:=	$(DATADIR)/man
LICDIR	:=	$(DATADIR)/licenses

.PHONY:	all tests

all:	stalld

stalld: $(OBJ)
	$(CC) -o stalld	 $(LDFLAGS) $(OBJ) $(LIBS)

static: $(OBJ)
	$(CC) -o stalld-static $(LDFLAGS) --static $(OBJ) $(LIBS)

tests:

.PHONY: install
install:
	$(INSTALL) -m 755 -d $(DESTDIR)$(BINDIR) $(DESTDIR)$(DOCDIR)
	$(INSTALL) stalld -m 755 $(DESTDIR)$(BINDIR)
	$(INSTALL) README.md -m 644 $(DESTDIR)$(DOCDIR)
	$(INSTALL) -m 755 -d $(DESTDIR)$(MANDIR)/man8
	$(INSTALL) man/stalld.8 -m 644 $(DESTDIR)$(MANDIR)/man8
	$(INSTALL) -m 755 -d $(DESTDIR)$(LICDIR)/$(NAME)
	$(INSTALL) gpl-2.0.txt -m 644 $(DESTDIR)$(LICDIR)/$(NAME)


.PHONY: clean tarball redhat push
clean:
	@test ! -f stalld || rm stalld
	@test ! -f stalld-static || rm stalld-static
	@test ! -f src/stalld.o || rm src/stalld.o
	@test ! -f $(TARBALL) || rm -f $(TARBALL)
	@make -C redhat VERSION=$(VERSION) clean
	@make -C tests clean
	@rm -rf *~ $(OBJ) *.tar.$(CEXT)

tarball:  clean
	rm -rf $(NAME)-$(VERSION) && mkdir $(NAME)-$(VERSION)
	cp -r $(DIRS) $(FILES) $(NAME)-$(VERSION)
	tar $(TAROPTS) --exclude='*~' $(NAME)-$(VERSION)
	rm -rf $(NAME)-$(VERSION)

redhat: tarball
	$(MAKE) -C redhat VERSION=$(VERSION) CEXT=$(CEXT)
